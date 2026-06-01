import os
import sys

import pytest

_BRIDGE_DIR = os.path.join(os.path.dirname(__file__), "..")
_PROTO_DIR = os.path.join(_BRIDGE_DIR, "proto")
sys.path.insert(0, _BRIDGE_DIR)
sys.path.insert(0, _PROTO_DIR)

SKIP_INTEGRATION = os.environ.get("CI", "") == "true"


import grpc  # noqa: E402

import overkill_pb2  # noqa: E402
from server import OverkillBridgeServicer  # noqa: E402


class FakeContext:
    """Test double for grpc.ServicerContext with the most commonly used methods."""

    def __init__(self) -> None:
        self.code = None
        self.details = None
        self._invocation_metadata: list[tuple[str, str]] = []
        self._trailing_metadata: list[tuple[str, str]] = []
        self._auth_context: dict[str, str] = {}
        self._peer = "test-peer"
        self._time_remaining = 60.0
        self._cancelled = False
        self._active = True
        self._callbacks: list = []

    def set_code(self, code: grpc.StatusCode) -> None:
        self.code = code

    def set_details(self, details: str) -> None:
        self.details = details

    def abort(self, code: grpc.StatusCode, details: str) -> None:
        self.code = code
        self.details = details
        raise grpc.RpcError(f"Aborted: {code} - {details}")

    def abort_with_status(self, status: grpc.Status) -> None:
        self.code = status.code
        self.details = status.details
        raise grpc.RpcError(f"Aborted with status: {status}")

    def invocation_metadata(self) -> list[tuple[str, str]]:
        return list(self._invocation_metadata)

    def set_trailing_metadata(self, trailing_metadata: list[tuple[str, str]]) -> None:
        self._trailing_metadata = list(trailing_metadata)

    def trailing_metadata(self) -> list[tuple[str, str]]:
        return list(self._trailing_metadata)

    def auth_context(self) -> dict[str, str]:
        return dict(self._auth_context)

    def peer(self) -> str:
        return self._peer

    def time_remaining(self) -> float:
        return self._time_remaining

    def cancel(self) -> None:
        self._cancelled = True

    def is_cancelled(self) -> bool:
        return self._cancelled

    def is_active(self) -> bool:
        return self._active

    def add_callback(self, callback) -> None:
        self._callbacks.append(callback)


def test_ping() -> None:
    servicer = OverkillBridgeServicer()
    resp = servicer.Ping(overkill_pb2.PingRequest(), FakeContext())
    assert resp.status == "ok"
    assert resp.version == "0.2.0"


@pytest.mark.skipif(SKIP_INTEGRATION, reason="ML model required, skipped in CI")
def test_embed() -> None:
    servicer = OverkillBridgeServicer()
    resp = servicer.Embed(overkill_pb2.EmbedRequest(text="hello world", model="test"), FakeContext())
    assert len(resp.embedding) > 0
    assert resp.tokens > 0


@pytest.mark.skipif(SKIP_INTEGRATION, reason="ML model required, skipped in CI")
def test_embed_batch() -> None:
    servicer = OverkillBridgeServicer()
    resp = servicer.EmbedBatch(
        overkill_pb2.EmbedBatchRequest(texts=["hello", "world"], model="test"), FakeContext()
    )
    assert len(resp.results) == 2
    for r in resp.results:
        assert len(r.embedding) > 0
        assert r.tokens > 0


@pytest.mark.skipif(SKIP_INTEGRATION, reason="ML model required, skipped in CI")
def test_rerank() -> None:
    servicer = OverkillBridgeServicer()
    resp = servicer.Rerank(
        overkill_pb2.RerankRequest(
            query="test query", documents=["doc one", "doc two", "doc three"], top_n=2
        ),
        FakeContext(),
    )
    assert len(resp.results) <= 3
    for r in resp.results:
        assert 0.0 <= r.relevance_score <= 1.0
        assert len(r.text) > 0


def test_store_and_search_vector() -> None:
    servicer = OverkillBridgeServicer()
    ctx = FakeContext()

    store_resp = servicer.StoreVector(
        overkill_pb2.StoreVectorRequest(
            entry=overkill_pb2.VectorEntry(
                id="v1",
                embedding=[0.1, 0.2, 0.3],
                content="test content",
                metadata={"type": "doc"},
            ),
            backend="inmem",
        ),
        ctx,
    )
    assert store_resp.success
    assert store_resp.id == "v1"

    search_resp = servicer.SearchVectors(
        overkill_pb2.SearchVectorsRequest(
            query=[0.1, 0.2, 0.3], top_k=10, threshold=0.0, backend="inmem"
        ),
        ctx,
    )
    assert len(search_resp.results) >= 1
    assert search_resp.results[0].id == "v1"


def test_search_with_filters() -> None:
    servicer = OverkillBridgeServicer()
    ctx = FakeContext()

    servicer.StoreVector(
        overkill_pb2.StoreVectorRequest(
            entry=overkill_pb2.VectorEntry(
                id="v1",
                embedding=[0.1, 0.2, 0.3],
                content="cat doc",
                metadata={"type": "cat"},
            ),
            backend="inmem",
        ),
        ctx,
    )
    servicer.StoreVector(
        overkill_pb2.StoreVectorRequest(
            entry=overkill_pb2.VectorEntry(
                id="v2",
                embedding=[0.1, 0.2, 0.3],
                content="dog doc",
                metadata={"type": "dog"},
            ),
            backend="inmem",
        ),
        ctx,
    )

    search_resp = servicer.SearchVectors(
        overkill_pb2.SearchVectorsRequest(
            query=[0.1, 0.2, 0.3], top_k=10, threshold=0.0, filters={"type": "cat"}
        ),
        ctx,
    )
    assert len(search_resp.results) == 1
    assert search_resp.results[0].id == "v1"


def test_delete_vector() -> None:
    servicer = OverkillBridgeServicer()
    ctx = FakeContext()

    servicer.StoreVector(
        overkill_pb2.StoreVectorRequest(
            entry=overkill_pb2.VectorEntry(
                id="v1", embedding=[0.1, 0.2, 0.3], content="test", metadata={}
            ),
            backend="inmem",
        ),
        ctx,
    )

    del_resp = servicer.DeleteVector(
        overkill_pb2.DeleteVectorRequest(id="v1", backend="inmem"), ctx
    )
    assert del_resp.success

    del_resp2 = servicer.DeleteVector(
        overkill_pb2.DeleteVectorRequest(id="nonexistent", backend="inmem"), ctx
    )
    assert not del_resp2.success


def test_compact() -> None:
    servicer = OverkillBridgeServicer()
    content = " ".join(f"word{i}" for i in range(200))
    resp = servicer.Compact(
        overkill_pb2.CompactRequest(
            content=content, model="test", target_tokens=50, style="detailed"
        ),
        FakeContext(),
    )
    assert resp.success
    assert len(resp.summary) > 0
    assert resp.original_tokens > 0
    assert resp.summary_tokens > 0


def test_compact_truncate() -> None:
    servicer = OverkillBridgeServicer()
    content = " ".join(f"word{i}" for i in range(200))
    resp = servicer.Compact(
        overkill_pb2.CompactRequest(
            content=content, model="test", target_tokens=50, style="truncate"
        ),
        FakeContext(),
    )
    assert resp.success
    assert resp.summary_tokens <= resp.original_tokens
