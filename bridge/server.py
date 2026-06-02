import logging
import os
import signal
import sys
import threading

sys.path.insert(0, os.path.dirname(__file__))

from concurrent import futures

import grpc  # noqa: E402

# grpc health check — standard health checking protocol
from grpc_health.v1 import health_pb2, health_pb2_grpc  # noqa: E402

import overkill_pb2  # noqa: E402
import overkill_pb2_grpc  # noqa: E402
from compaction.service import CompactionService  # noqa: E402
from embeddings.service import EmbeddingService  # noqa: E402
from memory.service import VectorMemoryService  # noqa: E402
from reranking.service import RerankingService  # noqa: E402

logger = logging.getLogger(__name__)

# Default memory backend: "postgres" for persistent storage.
# Falls back to "inmem" if DATABASE_URL is not set.
_DEFAULT_BACKEND = os.environ.get("OVERKILL_MEMORY_BACKEND", "postgres")
if _DEFAULT_BACKEND == "postgres" and not os.environ.get("DATABASE_URL"):
    logger.warning("DATABASE_URL not set — defaulting memory backend to 'inmem'")
    _DEFAULT_BACKEND = "inmem"


class OverkillBridgeServicer(overkill_pb2_grpc.OverkillBridgeServicer):
    def __init__(self) -> None:
        self._embeddings = EmbeddingService()
        self._reranking = RerankingService()
        self._memory = VectorMemoryService()
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
    """Standard gRPC health check service."""

    def Check(  # noqa: N802
        self, request: health_pb2.HealthCheckRequest, context: grpc.ServicerContext
    ) -> health_pb2.HealthCheckResponse:
        return health_pb2.HealthCheckResponse(status=health_pb2.HealthCheckResponse.SERVING)

    def Watch(  # noqa: N802
        self, request: health_pb2.HealthCheckRequest, context: grpc.ServicerContext
    ):
        # Not implemented — streaming health watch not needed.
        context.set_code(grpc.StatusCode.UNIMPLEMENTED)
        return health_pb2.HealthCheckResponse()


def serve(port: int = 50051) -> None:
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
    overkill_pb2_grpc.add_OverkillBridgeServicer_to_server(OverkillBridgeServicer(), server)
    health_pb2_grpc.add_HealthServicer_to_server(HealthServicer(), server)

    server.add_insecure_port(f"[::]:{port}")

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


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(name)s] %(levelname)s: %(message)s")
    serve()
