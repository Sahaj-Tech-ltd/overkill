from concurrent import futures

import grpc
import pytest
from grpc_health.v1 import health_pb2, health_pb2_grpc

import overkill_pb2 as pb
import overkill_pb2_grpc as rpc
from memory import postgres_backend
from memory.service import VectorMemoryService
from server import HealthServicer, OverkillBridgeServicer, _loopback_host


@pytest.fixture
def stub():
    bridge = OverkillBridgeServicer(allow_mock=True, vector_dimension=3)
    with futures.ThreadPoolExecutor(max_workers=4) as pool:
        server = grpc.server(pool)
        rpc.add_OverkillBridgeServicer_to_server(bridge, server)
        health_pb2_grpc.add_HealthServicer_to_server(HealthServicer(bridge), server)
        port = server.add_insecure_port("127.0.0.1:0")
        assert port
        server.start()
        try:
            with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
                grpc.channel_ready_future(channel).result(timeout=5)
                yield rpc.OverkillBridgeStub(channel), health_pb2_grpc.HealthStub(channel)
        finally:
            server.stop(0).wait()


@pytest.mark.parametrize("backend", ["inmem", "postgres"])
@pytest.mark.parametrize("vector", [[], [1], [1, 2, 3, 4], [0, 0, 0], [1, float('nan'), 2], [1, float('inf'), 2]])
def test_invalid_store_rejected_over_grpc(stub, backend, vector):
    client, _ = stub
    with pytest.raises(grpc.RpcError) as error:
        client.StoreVector(pb.StoreVectorRequest(
            entry=pb.VectorEntry(id="bad", embedding=vector), backend=backend,
        ), timeout=3)
    assert error.value.code() == grpc.StatusCode.INVALID_ARGUMENT


@pytest.mark.parametrize("backend", ["inmem", "postgres"])
@pytest.mark.parametrize("params", [
    {"top_k": -1}, {"top_k": 0}, {"top_k": 1001},
    {"threshold": float('nan')}, {"threshold": float('inf')}, {"threshold": 2},
    {"query": []}, {"query": [1, 2]}, {"query": [0, 0, 0]},
])
def test_invalid_search_rejected_over_grpc(stub, backend, params):
    client, _ = stub
    request = dict(query=[1, 2, 3], top_k=10, threshold=0, backend=backend)
    request.update(params)
    with pytest.raises(grpc.RpcError) as error:
        client.SearchVectors(pb.SearchVectorsRequest(**request), timeout=3)
    assert error.value.code() == grpc.StatusCode.INVALID_ARGUMENT


def test_inmem_copies_caller_data_and_limits_results():
    memory = VectorMemoryService(dimension=3)
    vector, metadata = [1, 2, 3], {"type": "original"}
    memory.store("a", vector, "first", metadata, backend="inmem")
    vector[:] = [3, 2, 1]
    metadata['type'] = 'changed'
    memory.store("b", [1, 2, 3], "second", {}, backend="inmem")
    results = memory.search([1, 2, 3], top_k=1, filters={"type": "original"}, backend="inmem")
    assert len(results) == 1
    assert results[0]['id'] == 'a'
    assert results[0]['score'] == pytest.approx(1)


def test_mock_is_degraded_health_with_live_ping(stub):
    client, health = stub
    assert client.Ping(pb.PingRequest(), timeout=3).status == "ok"
    assert len(client.Embed(pb.EmbedRequest(text="hello"), timeout=3).embedding) == 384
    for service in ("", "embeddings", "reranking"):
        response = health.Check(health_pb2.HealthCheckRequest(service=service), timeout=3)
        assert response.status == health_pb2.HealthCheckResponse.NOT_SERVING
    with pytest.raises(grpc.RpcError) as error:
        health.Check(health_pb2.HealthCheckRequest(service="missing"), timeout=3)
    assert error.value.code() == grpc.StatusCode.NOT_FOUND


@pytest.mark.parametrize("host", ["0.0.0.0", "::", "192.168.1.10", "example.com"])
def test_remote_bind_rejected(host):
    with pytest.raises(ValueError, match="no TLS/authentication"):
        _loopback_host(host)


@pytest.mark.parametrize("host", ["127.0.0.1", "::1", "localhost"])
def test_loopback_bind_allowed(host):
    assert _loopback_host(host) in ("127.0.0.1", "::1")


def test_direct_postgres_api_validates_before_connection(monkeypatch):
    def unexpected_connection():
        pytest.fail("invalid input opened a database connection")
    monkeypatch.setattr(postgres_backend, "_get_conn", unexpected_connection)
    with pytest.raises(ValueError, match="dimension"):
        postgres_backend.store("id", [1], "content", dimension=3)
    with pytest.raises(ValueError, match="top_k"):
        postgres_backend.search([1, 2, 3], top_k=-1, dimension=3)
