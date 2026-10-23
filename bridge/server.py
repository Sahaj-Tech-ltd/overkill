import argparse
import ipaddress
import logging
import os
import signal
import threading
from concurrent import futures

import grpc

# grpc health check — standard health checking protocol
from grpc_health.v1 import health_pb2, health_pb2_grpc

import overkill_pb2
import overkill_pb2_grpc
from compaction.service import CompactionService
from embeddings.service import EmbeddingService
from memory.service import VectorMemoryService
from model_runtime import ModelUnavailableError
from reranking.service import RerankingService

logger = logging.getLogger(__name__)

_DEFAULT_BACKEND = os.environ.get("OVERKILL_MEMORY_BACKEND", "postgres")

class OverkillBridgeServicer(overkill_pb2_grpc.OverkillBridgeServicer):
    def __init__(self, *, allow_mock: bool | None = None, vector_dimension: int | None = None) -> None:
        self._embeddings = EmbeddingService(allow_mock=allow_mock)
        self._reranking = RerankingService(allow_mock=allow_mock)
        self._memory = VectorMemoryService(dimension=vector_dimension)
        self._compaction = CompactionService()

    def Ping(self, request: overkill_pb2.PingRequest, context: grpc.ServicerContext) -> overkill_pb2.PongResponse:  # noqa: N802
        try:
            return overkill_pb2.PongResponse(status="ok", version="0.2.0")
        except Exception:
            logger.exception("Ping handler failed")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("Internal error processing Ping")
            return overkill_pb2.PongResponse(status="error", version="0.2.0")

    def Embed(self, request: overkill_pb2.EmbedRequest, context: grpc.ServicerContext) -> overkill_pb2.EmbedResponse:  # noqa: N802
        try:
            embedding, tokens = self._embeddings.embed(request.text, request.model)
            return overkill_pb2.EmbedResponse(embedding=embedding, tokens=tokens)
        except ModelUnavailableError as exc:
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            context.set_details(str(exc))
            return overkill_pb2.EmbedResponse()
        except Exception:
            logger.exception("Embed handler failed")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("Internal error during embedding")
            return overkill_pb2.EmbedResponse()

    def EmbedBatch(  # noqa: N802
        self, request: overkill_pb2.EmbedBatchRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.EmbedBatchResponse:
        try:
            results = self._embeddings.embed_batch(list(request.texts), request.model)
            pb_results = [
                overkill_pb2.EmbedResult(embedding=emb, tokens=tok) for emb, tok in results
            ]
            return overkill_pb2.EmbedBatchResponse(results=pb_results)
        except ModelUnavailableError as exc:
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            context.set_details(str(exc))
            return overkill_pb2.EmbedBatchResponse()
        except Exception:
            logger.exception("EmbedBatch handler failed")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("Internal error during batch embedding")
            return overkill_pb2.EmbedBatchResponse()

    def Rerank(self, request: overkill_pb2.RerankRequest, context: grpc.ServicerContext) -> overkill_pb2.RerankResponse:  # noqa: N802
        try:
            results = self._reranking.rerank(request.query, list(request.documents), request.top_n, request.model)
            pb_results = [
                overkill_pb2.RerankResult(index=idx, relevance_score=score, text=text)
                for idx, score, text in results
            ]
            return overkill_pb2.RerankResponse(results=pb_results)
        except ModelUnavailableError as exc:
            context.set_code(grpc.StatusCode.UNAVAILABLE)
            context.set_details(str(exc))
            return overkill_pb2.RerankResponse()
        except Exception:
            logger.exception("Rerank handler failed")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("Internal error during reranking")
            return overkill_pb2.RerankResponse()

    def StoreVector(  # noqa: N802
        self, request: overkill_pb2.StoreVectorRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.StoreVectorResponse:
        try:
            entry = request.entry
            backend = request.backend if request.backend else _DEFAULT_BACKEND
            stored_id = self._memory.store(
                entry.id, list(entry.embedding), entry.content, dict(entry.metadata), backend=backend
            )
            return overkill_pb2.StoreVectorResponse(id=stored_id, success=True)
        except ValueError as exc:
            logger.warning("StoreVector validation error: %s", exc)
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details(str(exc))
            return overkill_pb2.StoreVectorResponse(id="", success=False)
        except Exception:
            logger.exception("StoreVector handler failed")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("Internal error storing vector")
            return overkill_pb2.StoreVectorResponse(id="", success=False)

    def SearchVectors(  # noqa: N802
        self, request: overkill_pb2.SearchVectorsRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.SearchVectorsResponse:
        try:
            backend = request.backend if request.backend else _DEFAULT_BACKEND
            results = self._memory.search(
                list(request.query), request.top_k, request.threshold, dict(request.filters), backend=backend
            )
            pb_results = [
                overkill_pb2.SearchResult(
                    id=r["id"], score=r["score"], content=r["content"], metadata=r["metadata"]
                )
                for r in results
            ]
            return overkill_pb2.SearchVectorsResponse(results=pb_results)
        except ValueError as exc:
            logger.warning("SearchVectors validation error: %s", exc)
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details(str(exc))
            return overkill_pb2.SearchVectorsResponse()
        except Exception:
            logger.exception("SearchVectors handler failed")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("Internal error searching vectors")
            return overkill_pb2.SearchVectorsResponse()

    def DeleteVector(  # noqa: N802
        self, request: overkill_pb2.DeleteVectorRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.DeleteVectorResponse:
        try:
            backend = request.backend if request.backend else _DEFAULT_BACKEND
            ok = self._memory.delete(request.id, backend=backend)
            return overkill_pb2.DeleteVectorResponse(success=ok)
        except ValueError as exc:
            logger.warning("DeleteVector validation error: %s", exc)
            context.set_code(grpc.StatusCode.INVALID_ARGUMENT)
            context.set_details(str(exc))
            return overkill_pb2.DeleteVectorResponse(success=False)
        except Exception:
            logger.exception("DeleteVector handler failed")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("Internal error deleting vector")
            return overkill_pb2.DeleteVectorResponse(success=False)

    def Compact(  # noqa: N802
        self, request: overkill_pb2.CompactRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.CompactResponse:
        try:
            summary, original_tokens, summary_tokens = self._compaction.compact(
                request.content, request.model, request.target_tokens, request.style
            )
            return overkill_pb2.CompactResponse(
                summary=summary,
                original_tokens=original_tokens,
                summary_tokens=summary_tokens,
                success=True,
            )
        except Exception:
            logger.exception("Compact handler failed")
            context.set_code(grpc.StatusCode.INTERNAL)
            context.set_details("Internal error during compaction")
            return overkill_pb2.CompactResponse(success=False)


class HealthServicer(health_pb2_grpc.HealthServicer):
    """Readiness for semantic capabilities; Ping remains a liveness check."""

    def __init__(self, bridge: OverkillBridgeServicer) -> None:
        self._bridge = bridge

    def Check(self, request, context):  # noqa: N802
        services = {"embeddings": self._bridge._embeddings, "reranking": self._bridge._reranking}
        if request.service and request.service not in services:
            context.set_code(grpc.StatusCode.NOT_FOUND)
            context.set_details("Unknown capability; use embeddings or reranking")
            return health_pb2.HealthCheckResponse()
        selected = [services[request.service]] if request.service else list(services.values())
        ready = all(service.capability_status == "ready" for service in selected)
        return health_pb2.HealthCheckResponse(
            status=health_pb2.HealthCheckResponse.SERVING if ready else health_pb2.HealthCheckResponse.NOT_SERVING
        )

    def Watch(self, request, context):  # noqa: N802
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        context.set_details("Use unary Check for readiness")
        return iter(())


def _loopback_host(host: str) -> str:
    if host == "localhost":
        return "127.0.0.1"
    try:
        if ipaddress.ip_address(host).is_loopback:
            return host
    except ValueError:
        pass
    raise ValueError(
        "Remote bridge binding is unsupported: this server has no TLS/authentication. "
        "Use a loopback address (127.0.0.1 or ::1); remote exposure requires an authenticated TLS proxy."
    )


def serve(port: int = 50051, host: str = "127.0.0.1") -> None:
    host = _loopback_host(host)
    logger.info("Starting Overkill bridge server on port %d", port)

    # Server options for production readiness.
    max_msg_mb = 10
    options = [
        ("grpc.max_send_message_length", max_msg_mb * 1024 * 1024),
        ("grpc.max_receive_message_length", max_msg_mb * 1024 * 1024),
        ("grpc.keepalive_time_ms", 30000),
        ("grpc.keepalive_timeout_ms", 10000),
        ("grpc.keepalive_permit_without_calls", True),
        ("grpc.http2.max_pings_without_data", 0),
    ]

    executor = futures.ThreadPoolExecutor(max_workers=10)
    server = grpc.server(executor, options=options)

    # Register services.
    bridge = OverkillBridgeServicer()
    overkill_pb2_grpc.add_OverkillBridgeServicer_to_server(bridge, server)
    health_pb2_grpc.add_HealthServicer_to_server(HealthServicer(bridge), server)

    address = f"[{host}]:{port}" if ":" in host else f"{host}:{port}"
    if not server.add_insecure_port(address):
        executor.shutdown(wait=True)
        raise RuntimeError(f"Could not bind bridge to {address}")

    stop_event = threading.Event()

    def _signal_handler(signum: int, frame: object) -> None:
        logger.info("Received signal %d — initiating graceful shutdown", signum)
        stop_event.set()

    signal.signal(signal.SIGTERM, _signal_handler)
    signal.signal(signal.SIGINT, _signal_handler)

    server.start()
    logger.info("Bridge server started on port %d (default backend=%s)", port, _DEFAULT_BACKEND)

    try:
        stop_event.wait()
    except KeyboardInterrupt:
        pass
    finally:
        logger.info("Shutting down bridge server")
        server.stop(grace=5).wait()
        executor.shutdown(wait=True)
        logger.info("Bridge server stopped")


def main() -> None:
    parser = argparse.ArgumentParser(description="Local Overkill Python bridge")
    parser.add_argument("--host", default="127.0.0.1", help="Loopback IP only; remote binding is unsupported")
    parser.add_argument("--port", type=int, default=50051)
    args = parser.parse_args()
    if not 1 <= args.port <= 65535:
        parser.error("port must be between 1 and 65535")
    try:
        _loopback_host(args.host)
    except ValueError as exc:
        parser.error(str(exc))
    logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(name)s] %(levelname)s: %(message)s")
    serve(args.port, args.host)


if __name__ == "__main__":
    main()
