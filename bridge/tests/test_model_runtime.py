"""Model lifecycle tests use controllable CPU stubs, never network/model downloads."""
import sys
import threading
from concurrent.futures import ThreadPoolExecutor
from types import SimpleNamespace

import pytest

from embeddings.service import EmbeddingService
from model_runtime import ModelUnavailableError
from reranking.service import RerankingService


class Vector(list):
    def tolist(self):
        return list(self)


@pytest.fixture(params=["embed", "embed_batch", "rerank"])
def semantic(request, monkeypatch):
    cls = RerankingService if request.param == "rerank" else EmbeddingService
    service = cls(allow_mock=False)
    service._test_batch = request.param == "embed_batch"
    monkeypatch.setattr(service, "_detect_device", lambda: "cpu")
    return service


def infer(service, name):
    if isinstance(service, EmbeddingService):
        if getattr(service, "_test_batch", False):
            return service.embed_batch(["text"], name)[0][0][0]
        return service.embed("text", name)[0][0]
    return service.rerank("query", ["doc"], model=name)[0][1]


def install_stub(monkeypatch, factory):
    monkeypatch.setitem(sys.modules, "sentence_transformers", SimpleNamespace(
        SentenceTransformer=factory, CrossEncoder=factory,
    ))


def test_failed_replacement_not_published_or_reused(semantic, monkeypatch):
    loads = []

    class Model:
        def __init__(self, name, **kwargs):
            loads.append(name)
            if name == "broken":
                raise OSError("model file unreadable")

        def get_sentence_embedding_dimension(self):
            return 1

        def encode(self, texts, **kwargs):
            return [Vector([7.0]) for _ in texts] if isinstance(texts, list) else Vector([7.0])

        def predict(self, pairs):
            return [7.0] * len(pairs)

    install_stub(monkeypatch, Model)
    assert infer(semantic, "good") == 7
    for _ in range(2):
        with pytest.raises(ModelUnavailableError, match="Could not load broken"):
            infer(semantic, "broken")
        assert semantic._loaded_model_name == "good"
        assert semantic.capability_status == "unavailable"
    assert loads == ["good", "broken", "broken"]
    assert infer(semantic, "good") == 7


def test_switch_waits_for_inflight_inference(semantic, monkeypatch):
    entered = threading.Event()
    release = threading.Event()
    second_started = threading.Event()
    loaded_second = threading.Event()

    class Model:
        def __init__(self, name, **kwargs):
            self.name = name
            if name == "second":
                loaded_second.set()

        def get_sentence_embedding_dimension(self):
            return 1

        def value(self):
            if self.name == "first":
                entered.set()
                assert release.wait(5), "test did not release first inference"
            return 1.0 if self.name == "first" else 2.0

        def encode(self, texts, **kwargs):
            value = self.value()
            return [Vector([value]) for _ in texts] if isinstance(texts, list) else Vector([value])

        def predict(self, pairs):
            return [self.value()] * len(pairs)

    install_stub(monkeypatch, Model)

    def second():
        second_started.set()
        return infer(semantic, "second")

    with ThreadPoolExecutor(max_workers=2) as pool:
        first = pool.submit(infer, semantic, "first")
        assert entered.wait(5)
        other = pool.submit(second)
        try:
            assert second_started.wait(5)
            assert not loaded_second.wait(0.05), "replacement loaded while old model was in use"
        finally:
            release.set()
        assert first.result(timeout=5) == 1
        assert other.result(timeout=5) == 2
    assert semantic._loaded_model_name == "second"


def test_missing_dependency_fails_without_mock(semantic, monkeypatch):
    monkeypatch.setitem(sys.modules, "sentence_transformers", None)
    with pytest.raises(ModelUnavailableError, match=r"\[ml\] extra"):
        infer(semantic, "required")
    assert not semantic._initialized
    assert semantic._loaded_model_name == ""
    assert semantic.capability_status == "unavailable"


@pytest.mark.parametrize("service_cls", [EmbeddingService, RerankingService])
def test_explicit_mock_never_imports_real_models(service_cls, monkeypatch):
    monkeypatch.setitem(sys.modules, "sentence_transformers", None)
    service = service_cls(allow_mock=True)
    infer(service, "mock")
    assert service.capability_status == "mock"


def test_mock_requires_explicit_environment_opt_in(monkeypatch):
    monkeypatch.delenv("OVERKILL_ALLOW_MOCK_MODELS", raising=False)
    assert not EmbeddingService()._allow_mock
    monkeypatch.setenv("OVERKILL_ALLOW_MOCK_MODELS", "1")
    assert EmbeddingService()._allow_mock


def test_health_tracks_real_model_load_failures_and_recovery(monkeypatch):
    from grpc_health.v1 import health_pb2

    from server import HealthServicer, OverkillBridgeServicer

    class Model:
        def __init__(self, name, **kwargs):
            if name == "broken":
                raise OSError("broken model")

        def get_sentence_embedding_dimension(self):
            return 1

        def encode(self, *args, **kwargs):
            return Vector([1.0])

        def predict(self, pairs):
            return [1.0] * len(pairs)

    install_stub(monkeypatch, Model)
    bridge = OverkillBridgeServicer(allow_mock=False)
    monkeypatch.setattr(bridge._embeddings, "_detect_device", lambda: "cpu")
    monkeypatch.setattr(bridge._reranking, "_detect_device", lambda: "cpu")
    health = HealthServicer(bridge)
    request = health_pb2.HealthCheckRequest()
    assert health.Check(request, None).status == health_pb2.HealthCheckResponse.NOT_SERVING
    bridge._embeddings.embed("text")
    embedding_health = health.Check(health_pb2.HealthCheckRequest(service="embeddings"), None)
    assert embedding_health.status == health_pb2.HealthCheckResponse.SERVING
    bridge._reranking.rerank("query", ["doc"])
    assert health.Check(request, None).status == health_pb2.HealthCheckResponse.SERVING
    with pytest.raises(ModelUnavailableError):
        bridge._embeddings.embed("text", "broken")
    assert health.Check(request, None).status == health_pb2.HealthCheckResponse.NOT_SERVING
    bridge._embeddings.embed("text")
    assert health.Check(request, None).status == health_pb2.HealthCheckResponse.SERVING
