import os
import sys

sys.path.insert(0, os.path.dirname(__file__))

from concurrent import futures

import grpc  # noqa: E402
import overkill_pb2  # noqa: E402
import overkill_pb2_grpc  # noqa: E402

from compaction.service import CompactionService  # noqa: E402
from embeddings.service import EmbeddingService  # noqa: E402
from memory.service import VectorMemoryService  # noqa: E402
from reranking.service import RerankingService  # noqa: E402


class OverkillBridgeServicer(overkill_pb2_grpc.OverkillBridgeServicer):
    def __init__(self) -> None:
        self._embeddings = EmbeddingService()
        self._reranking = RerankingService()
        self._memory = VectorMemoryService()
        self._compaction = CompactionService()

    def Ping(self, request: overkill_pb2.PingRequest, context: grpc.ServicerContext) -> overkill_pb2.PongResponse:  # noqa: N802
        return overkill_pb2.PongResponse(status="ok", version="0.1.0")

    def Embed(self, request: overkill_pb2.EmbedRequest, context: grpc.ServicerContext) -> overkill_pb2.EmbedResponse:  # noqa: N802
        embedding, tokens = self._embeddings.embed(request.text, request.model)
        return overkill_pb2.EmbedResponse(embedding=embedding, tokens=tokens)

    def EmbedBatch(  # noqa: N802
        self, request: overkill_pb2.EmbedBatchRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.EmbedBatchResponse:
        results = self._embeddings.embed_batch(list(request.texts), request.model)
        pb_results = [
            overkill_pb2.EmbedResult(embedding=emb, tokens=tok) for emb, tok in results
        ]
        return overkill_pb2.EmbedBatchResponse(results=pb_results)

    def Rerank(self, request: overkill_pb2.RerankRequest, context: grpc.ServicerContext) -> overkill_pb2.RerankResponse:  # noqa: N802
        results = self._reranking.rerank(request.query, list(request.documents), request.top_n)
        pb_results = [
            overkill_pb2.RerankResult(index=idx, relevance_score=score, text=text)
            for idx, score, text in results
        ]
        return overkill_pb2.RerankResponse(results=pb_results)

    def StoreVector(  # noqa: N802
        self, request: overkill_pb2.StoreVectorRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.StoreVectorResponse:
        entry = request.entry
        backend = request.backend if request.backend else "inmem"
        stored_id = self._memory.store(
            entry.id, list(entry.embedding), entry.content, dict(entry.metadata), backend=backend
        )
        return overkill_pb2.StoreVectorResponse(id=stored_id, success=True)

    def SearchVectors(  # noqa: N802
        self, request: overkill_pb2.SearchVectorsRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.SearchVectorsResponse:
        backend = request.backend if request.backend else "inmem"
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

    def DeleteVector(  # noqa: N802
        self, request: overkill_pb2.DeleteVectorRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.DeleteVectorResponse:
        backend = request.backend if request.backend else "inmem"
        ok = self._memory.delete(request.id, backend=backend)
        return overkill_pb2.DeleteVectorResponse(success=ok)

    def Compact(  # noqa: N802
        self, request: overkill_pb2.CompactRequest, context: grpc.ServicerContext
    ) -> overkill_pb2.CompactResponse:
        summary, original_tokens, summary_tokens = self._compaction.compact(
            request.content, request.model, request.target_tokens, request.style
        )
        return overkill_pb2.CompactResponse(
            summary=summary,
            original_tokens=original_tokens,
            summary_tokens=summary_tokens,
            success=True,
        )


def serve(port: int = 50051) -> None:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    overkill_pb2_grpc.add_OverkillBridgeServicer_to_server(OverkillBridgeServicer(), server)
    server.add_insecure_port(f"[::]:{port}")
    server.start()
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
# test
