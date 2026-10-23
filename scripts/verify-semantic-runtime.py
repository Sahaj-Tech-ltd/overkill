#!/usr/bin/env python3
"""Real offline model + gRPC + pgvector acceptance (no mocks or paid APIs).

Run with the bridge[ml] Python interpreter, pinned snapshot manifest, and the
semantic validation state file. --restart-postgres explicitly authorizes only
`postgres` in that state's overkill-semantic-* compose project to restart.
The only runtime fixture is _detect_device -> cpu/mps to exercise each device.
"""
from __future__ import annotations

import argparse
import concurrent.futures
import hashlib
import importlib.metadata
import json
import math
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import traceback
import uuid

ROOT = Path(__file__).resolve().parents[1]
sys.path[:0] = [str(ROOT / "bridge"), str(ROOT / "bridge/proto")]


def offline_environment():
    os.environ.update({"HF_HUB_OFFLINE": "1", "TRANSFORMERS_OFFLINE": "1",
        "HF_HUB_DISABLE_IMPLICIT_TOKEN": "1", "HF_HUB_DISABLE_TELEMETRY": "1",
        "DO_NOT_TRACK": "1", "OVERKILL_ALLOW_MOCK_MODELS": "0",
        "TOKENIZERS_PARALLELISM": "false", "PYTORCH_ENABLE_MPS_FALLBACK": "0"})
    for key in ("HF_TOKEN", "HUGGING_FACE_HUB_TOKEN", "HUGGINGFACE_HUB_TOKEN"):
        os.environ.pop(key, None)


def ensure(condition, message):
    if not condition:
        raise AssertionError(message)


def worker(device, proof_path):
    offline_environment()
    from concurrent import futures
    import grpc
    import torch
    import overkill_pb2_grpc as rpc
    from grpc_health.v1 import health_pb2_grpc
    import server as production
    ensure(Path(production.__file__).resolve() == ROOT / "bridge/server.py", "worker imported stale bridge")
    if device == "mps":
        ensure(torch.backends.mps.is_available(), "MPS hardware is unavailable")
    # TEST DEVICE FIXTURE ONLY: all model construction/inference remains real.
    production.EmbeddingService._detect_device = lambda self: device
    production.RerankingService._detect_device = lambda self: device
    executor = futures.ThreadPoolExecutor(max_workers=10)
    service = production.OverkillBridgeServicer(allow_mock=False, vector_dimension=384)
    server = grpc.server(executor)
    rpc.add_OverkillBridgeServicer_to_server(service, server)
    health_pb2_grpc.add_HealthServicer_to_server(production.HealthServicer(service), server)
    port = server.add_insecure_port("127.0.0.1:0")
    ensure(port > 0, "gRPC loopback listener failed")
    import threading
    stopped = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stopped.set())
    signal.signal(signal.SIGINT, lambda *_: stopped.set())
    server.start()
    print(json.dumps({"port": port, "pid": os.getpid(), "source": str(Path(production.__file__).resolve())}), flush=True)
    try:
        stopped.wait()
    finally:
        server.stop(grace=5).wait()
        executor.shutdown(wait=True)
        evidence = {"pid": os.getpid(), "device_fixture": device, "mock_allowed": False}
        for name, obj in (("embedding", service._embeddings), ("reranking", service._reranking)):
            model = obj._model
            module = getattr(model, "model", model)
            parameter_device = str(next(module.parameters()).device) if module is not None else None
            evidence[name] = {"class": type(model).__module__ + "." + type(model).__name__,
                "parameter_device": parameter_device, "selected_device": obj._device,
                "capability": obj.capability_status, "model": obj._loaded_model_name}
        Path(proof_path).write_text(json.dumps(evidence, indent=2) + "\n")


class Bridge:
    def __init__(self, device, dsn, directory):
        self.proof = directory / (device + "-" + uuid.uuid4().hex + ".json")
        self.log = self.proof.with_suffix(".log")
        environment = os.environ.copy()
        environment["DATABASE_URL"] = dsn
        self.log_handle = self.log.open("w")
        self.process = subprocess.Popen([sys.executable, str(Path(__file__).resolve()),
            "--worker", device, "--proof", str(self.proof)], cwd=ROOT,
            env=environment, stdout=subprocess.PIPE, stderr=self.log_handle, text=True)
        import select
        ready, _, _ = select.select([self.process.stdout], [], [], 90)
        ensure(bool(ready), "bridge worker startup timed out")
        line = self.process.stdout.readline()
        ensure(bool(line), f"bridge worker exited before readiness; see {self.log}")
        self.ready = json.loads(line)
        import grpc
        import overkill_pb2_grpc as rpc
        from grpc_health.v1 import health_pb2_grpc
        self.channel = grpc.insecure_channel(f"127.0.0.1:{self.ready['port']}")
        grpc.channel_ready_future(self.channel).result(timeout=10)
        self.stub = rpc.OverkillBridgeStub(self.channel)
        self.health_stub = health_pb2_grpc.HealthStub(self.channel)

    def close(self):
        self.channel.close()
        self.process.terminate()
        try:
            self.process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait(timeout=5)
            raise AssertionError("bridge failed graceful shutdown")
        finally:
            self.log_handle.close()
        ensure(self.process.returncode == 0, f"worker exited {self.process.returncode}; see {self.log}")
        return json.loads(self.proof.read_text())


def main(args):
    offline_environment()
    import grpc
    import psycopg2
    from psycopg2 import sql
    from psycopg2.extensions import make_dsn
    import overkill_pb2 as pb
    from grpc_health.v1 import health_pb2 as hp
    from datetime import datetime, timezone
    started = time.monotonic()
    state = json.loads(Path(args.state).read_text())
    manifest = json.loads(Path(args.snapshots).read_text())
    directory = Path(args.receipt).resolve().parent
    directory.mkdir(parents=True, exist_ok=True)
    receipt = {"format": 1, "started_utc": datetime.now(timezone.utc).isoformat(),
        "python": sys.version, "source_root": str(ROOT), "model_manifest": str(Path(args.snapshots).resolve()),
        "model_revisions": [{"role": m["role"], "repository": m["repository"], "revision": m["revision"]} for m in manifest],
        "device_fixture": "Only _detect_device is overridden per worker (cpu or mps); actual SentenceTransformer/CrossEncoder tensors and inference, MPS fallback disabled.",
        "offline": True, "mock_models": False, "paid_calls": False,
        "model_selection_scope": "Explicit pinned local snapshot paths; default remote model aliases/download are not validated.",
        "postgres": {k: state[k] for k in ("project", "port", "image")},
        "status": "running", "cases": [], "workers": [],
        "script_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    receipt["packages"] = {name: importlib.metadata.version(name) for name in
        ("torch", "sentence-transformers", "transformers", "grpcio", "psycopg2-binary", "pgvector", "numpy")}
    receipt["source_sha256"] = {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
        for p in [ROOT / "bridge/server.py", ROOT / "bridge/embeddings/service.py", ROOT / "bridge/reranking/service.py",
                  ROOT / "bridge/memory/service.py", ROOT / "bridge/memory/postgres_backend.py", ROOT / "bridge/memory/validation.py",
                  ROOT / "bridge/model_runtime.py", ROOT / "bridge/proto/overkill_pb2.py", ROOT / "bridge/proto/overkill_pb2_grpc.py"]}
    schema = "semantic_acceptance_" + uuid.uuid4().hex
    dsn = make_dsn(state["dsn"], options=f"-csearch_path={schema},public")
    models = {m["role"]: m["path"] for m in manifest}
    os.environ["DATABASE_URL"] = dsn
    expected_cases = {"pinned_model_checksums", "grpc_pgvector_store_search_filter_validation",
        "persistent_pgvector_dimension_contract", "actual_postgres_container_restart_persistence",
        "fresh_bridge_process_persistence_and_delete"}
    for device in ("cpu", "mps"):
        expected_cases.update(device + "_" + suffix for suffix in ("real_embedding_and_batch",
            "real_chunked_rerank_and_grpc_health", "concurrent_384_768_model_switches", "failed_model_health_and_recovery"))

    def case(name, operation):
        begin = time.monotonic()
        try:
            detail = operation()
            row = {"name": name, "status": "passed", "detail": detail}
        except Exception as exc:
            error = str(exc).replace(state["dsn"], "[redacted test DSN]")
            row = {"name": name, "status": "failed", "error": error,
                   "traceback": traceback.format_exc().replace(state["dsn"], "[redacted test DSN]")}
        row["seconds"] = round(time.monotonic() - begin, 3)
        receipt["cases"].append(row)
        print(json.dumps({k: row[k] for k in ("name", "status", "seconds")}), flush=True)
        Path(args.receipt).write_text(json.dumps(receipt, indent=2) + "\n")
        return row["status"] == "passed"

    def verify_snapshots():
        total = 0
        for model in manifest:
            for file in model["files"]:
                path = Path(model["path"]) / file["path"]
                ensure(path.stat().st_size == file["bytes"], f"snapshot size mismatch: {path}")
                ensure(hashlib.sha256(path.read_bytes()).hexdigest() == file["sha256"], f"snapshot hash mismatch: {path}")
                total += 1
        return {"files_verified": total}

    ensure(case("pinned_model_checksums", verify_snapshots), "pinned model verification failed")
    connection = psycopg2.connect(state["dsn"], connect_timeout=10)
    connection.autocommit = True
    with connection.cursor() as cursor:
        cursor.execute("CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public")
        cursor.execute(sql.SQL("CREATE SCHEMA {} ").format(sql.Identifier(schema)))
        cursor.execute("SELECT version(), extversion, pg_postmaster_start_time() FROM pg_extension WHERE extname='vector'")
        version, extension, before_restart = cursor.fetchone()
        receipt["postgres"].update({"server_version": version, "pgvector_version": extension,
                                   "schema": schema, "postmaster_before": before_restart.isoformat()})
    connection.close()
    vectors = {}

    def health(bridge, capability, expected):
        result = bridge.health_stub.Check(hp.HealthCheckRequest(service=capability), timeout=15)
        ensure(result.status == expected, f"health {capability!r}: {result.status}, expected {expected}")

    def validate_vector(vector, dimension):
        ensure(len(vector) == dimension, f"dimension {len(vector)} != {dimension}")
        ensure(all(math.isfinite(value) for value in vector), "non-finite inference output")
        norm = math.sqrt(sum(value * value for value in vector))
        ensure(abs(norm - 1) < 0.0002, f"embedding is not normalized: {norm}")
        return norm

    def expect_code(operation, expected):
        try:
            operation()
        except grpc.RpcError as exc:
            ensure(exc.code() == expected, f"gRPC {exc.code()} != {expected}")
            return
        raise AssertionError(f"expected gRPC {expected}, got successful result")

    try:
        for device in ("cpu", "mps"):
            bridge = Bridge(device, dsn, directory)
            try:
                def embeddings():
                    ensure(bridge.stub.Ping(pb.PingRequest(), timeout=15).status == "ok", "liveness failed")
                    health(bridge, "", hp.HealthCheckResponse.NOT_SERVING)
                    health(bridge, "embeddings", hp.HealthCheckResponse.NOT_SERVING)
                    details = []
                    for role, dimension in (("embedding", 384), ("alternate_embedding", 768)):
                        one = bridge.stub.Embed(pb.EmbedRequest(text="Why do cats purr?", model=models[role]), timeout=120)
                        validate_vector(one.embedding, dimension)
                        batch = bridge.stub.EmbedBatch(pb.EmbedBatchRequest(texts=["Why do cats purr?", "A cat purrs on the sofa.", "PostgreSQL stores database rows."], model=models[role]), timeout=120)
                        ensure(len(batch.results) == 3, "batch lost rows")
                        for result in batch.results:
                            validate_vector(result.embedding, dimension)
                            ensure(result.tokens > 0, "missing token count")
                        query = list(one.embedding)
                        cosine = [sum(a*b for a,b in zip(query, result.embedding, strict=True)) for result in batch.results]
                        ensure(cosine[0] > 0.999, "single and batch disagree")
                        ensure(cosine[1] > cosine[2], "related text should outrank unrelated database text")
                        if device == "cpu" and dimension == 384:
                            vectors.update(query=query, cat=list(batch.results[1].embedding), database=list(batch.results[2].embedding))
                        details.append({"dimension": dimension, "semantic_cosines": cosine, "single_and_batch": True})
                    health(bridge, "embeddings", hp.HealthCheckResponse.SERVING)
                    return details
                case(device + "_real_embedding_and_batch", embeddings)

                def ranking():
                    docs = ["PostgreSQL stores database rows."] * 36
                    docs[33] = "A cat purrs when relaxed and comfortable."
                    result = bridge.stub.Rerank(pb.RerankRequest(query="Why do cats purr?", documents=docs, model=models["reranking"], top_n=3), timeout=120)
                    ensure(len(result.results) == 3, "reranker top_n ignored")
                    ensure(result.results[0].index == 33, "reranker missed relevant document across 32-item chunk boundary")
                    scores = [r.relevance_score for r in result.results]
                    ensure(all(math.isfinite(s) for s in scores), "non-finite reranker score")
                    ensure(scores == sorted(scores, reverse=True), "reranker output unsorted")
                    health(bridge, "reranking", hp.HealthCheckResponse.SERVING)
                    health(bridge, "", hp.HealthCheckResponse.SERVING)
                    return {"documents": len(docs), "top_index": 33, "scores": scores}
                case(device + "_real_chunked_rerank_and_grpc_health", ranking)

                def concurrent_switches():
                    roles = ["embedding", "alternate_embedding"] * 4
                    def infer(index_role):
                        index, role = index_role
                        result = bridge.stub.EmbedBatch(pb.EmbedBatchRequest(texts=[f"cats purr {index}", "database rows"], model=models[role]), timeout=180)
                        dimension = 384 if role == "embedding" else 768
                        ensure(len(result.results) == 2, "concurrent batch lost rows")
                        for row in result.results:
                            validate_vector(row.embedding, dimension)
                        return dimension
                    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
                        dimensions = list(pool.map(infer, enumerate(roles)))
                    ensure(dimensions == [384, 768] * 4, "concurrent model selections leaked dimensions")
                    return {"parallel_requests": 8, "rows": 16, "dimensions": dimensions}
                case(device + "_concurrent_384_768_model_switches", concurrent_switches)

                def failure_recovery():
                    missing = str(directory / "nonexistent-offline-model")
                    for kind in ("embedding", "reranking"):
                        if kind == "embedding":
                            bad = lambda: bridge.stub.Embed(pb.EmbedRequest(text="cats", model=missing), timeout=30)
                            good = lambda: bridge.stub.Embed(pb.EmbedRequest(text="cats", model=models["embedding"]), timeout=120)
                            capability = "embeddings"
                        else:
                            bad = lambda: bridge.stub.Rerank(pb.RerankRequest(query="cats", documents=["cats purr"], model=missing), timeout=30)
                            good = lambda: bridge.stub.Rerank(pb.RerankRequest(query="cats", documents=["cats purr"], model=models["reranking"]), timeout=120)
                            capability = "reranking"
                        expect_code(bad, grpc.StatusCode.UNAVAILABLE)
                        if kind == "embedding":
                            expect_code(lambda: bridge.stub.EmbedBatch(pb.EmbedBatchRequest(texts=["cats"], model=missing), timeout=30), grpc.StatusCode.UNAVAILABLE)
                        health(bridge, capability, hp.HealthCheckResponse.NOT_SERVING)
                        health(bridge, "", hp.HealthCheckResponse.NOT_SERVING)
                        ensure(bridge.stub.Ping(pb.PingRequest(), timeout=10).status == "ok", "failed load broke liveness")
                        good()
                        health(bridge, capability, hp.HealthCheckResponse.SERVING)
                    health(bridge, "", hp.HealthCheckResponse.SERVING)
                    return {"embedding_and_reranking": "UNAVAILABLE -> NOT_SERVING -> real model reload -> SERVING", "ping": "alive during failure"}
                case(device + "_failed_model_health_and_recovery", failure_recovery)

                if device == "cpu":
                    def memory():
                        ensure(bool(vectors), "real CPU vectors unavailable")
                        for identity, text, group in (("cat", "A cat purrs on the sofa.", "animals"), ("database", "PostgreSQL stores database rows.", "technology")):
                            response = bridge.stub.StoreVector(pb.StoreVectorRequest(entry=pb.VectorEntry(id=identity, embedding=vectors[identity], content=text, metadata={"group": group}), backend="postgres"), timeout=20)
                            ensure(response.success and response.id == identity, "pgvector write failed")
                        request = pb.SearchVectorsRequest(query=vectors["query"], top_k=2, threshold=-1, backend="postgres")
                        result = bridge.stub.SearchVectors(request, timeout=20)
                        ensure([row.id for row in result.results] == ["cat", "database"], "semantic pgvector order incorrect")
                        request.filters["group"] = "technology"
                        ensure([row.id for row in bridge.stub.SearchVectors(request, timeout=20).results] == ["database"], "metadata filter ignored")
                        for invalid in ([1.0] * 768, [float("nan")] * 384, [float("inf")] * 384):
                            expect_code(lambda: bridge.stub.StoreVector(pb.StoreVectorRequest(entry=pb.VectorEntry(id="invalid", embedding=invalid), backend="postgres"), timeout=20), grpc.StatusCode.INVALID_ARGUMENT)
                            expect_code(lambda: bridge.stub.SearchVectors(pb.SearchVectorsRequest(query=invalid, top_k=1, backend="postgres"), timeout=20), grpc.StatusCode.INVALID_ARGUMENT)
                        return {"stored": 2, "top_match": "cat", "filtered_match": "database", "rejected": ["768 dimensions", "NaN", "Infinity"]}
                    case("grpc_pgvector_store_search_filter_validation", memory)
                    def persistent_dimension():
                        from memory.postgres_backend import PostgresVectorBackend
                        wrong = PostgresVectorBackend(dimension=768)
                        try:
                            wrong.count()
                        except ValueError as exc:
                            ensure("Database vector dimension is 384, requested 768" in str(exc), "unexpected dimension failure")
                        else:
                            raise AssertionError("persistent 384-dimension contract accepted a 768-dimension backend")
                        ensure(PostgresVectorBackend(dimension=384).count() == 2, "failed dimension switch damaged real vectors")
                        return {"persisted_dimension": 384, "rejected_backend_dimension": 768, "existing_vectors_unchanged": 2}
                    case("persistent_pgvector_dimension_contract", persistent_dimension)
            finally:
                evidence = bridge.close()
                receipt["workers"].append(evidence)
                for kind in ("embedding", "reranking"):
                    ensure(evidence[kind]["parameter_device"].split(":")[0] == device, f"{kind} inference did not reside on {device}")

        def restart():
            ensure(args.restart_postgres, "--restart-postgres required for full acceptance")
            ensure(state["project"].startswith("overkill-semantic-"), "refusing to restart a nonsemantic project")
            compose = Path(state["compose"]).resolve()
            ensure(compose.parent == ROOT / ".cache/container-runtime/context", "unexpected compose context")
            command = [str(ROOT / ".cache/container-runtime/lima-2.2.1/bin/limactl"), "shell", "--workdir", str(compose.parent), "ok-build", "docker", "compose", "-f", compose.name, "-p", state["project"], "restart", "postgres"]
            environment = os.environ.copy()
            environment["LIMA_HOME"] = str(ROOT / ".cache/lima")
            image_command = command[:-2] + ["images", "--format", "json"]
            image_result = subprocess.run(image_command, env=environment, capture_output=True, text=True, timeout=30)
            ensure(image_result.returncode == 0, "could not inspect semantic postgres image")
            images = json.loads(image_result.stdout)
            ensure(len(images) == 1 and images[0]["Repository"] == "pgvector/pgvector", "unexpected semantic container image")
            ensure(images[0]["ID"] == state["image"].split("@", 1)[1], "running container differs from pinned pgvector digest")
            receipt["postgres"]["actual_container_image"] = images[0]
            completed = subprocess.run(command, env=environment, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=90)
            ensure(completed.returncode == 0, "semantic postgres container restart failed: " + completed.stdout[-2000:])
            deadline = time.monotonic() + 60
            after = None
            while time.monotonic() < deadline:
                try:
                    connection = psycopg2.connect(dsn, connect_timeout=2)
                    with connection.cursor() as cursor:
                        cursor.execute("SELECT pg_postmaster_start_time(),count(*) FROM vector_memories GROUP BY 1")
                        after, count = cursor.fetchone()
                        ensure(count == 2, "container restart lost stored real vectors")
                    connection.close()
                    if after > before_restart:
                        break
                except psycopg2.Error:
                    time.sleep(0.5)
            ensure(after is not None and after > before_restart, "database postmaster did not actually restart")
            receipt["postgres"]["postmaster_after"] = after.isoformat()
            return {"service": "postgres", "same_compose_project_and_volume": True, "retained_vectors": count}
        case("actual_postgres_container_restart_persistence", restart)

        bridge = Bridge("cpu", dsn, directory)
        try:
            def fresh_process():
                result = bridge.stub.SearchVectors(pb.SearchVectorsRequest(query=vectors["query"], top_k=2, threshold=-1, backend="postgres"), timeout=20)
                ensure([row.id for row in result.results] == ["cat", "database"], "fresh bridge process did not find preserved vectors")
                ensure(bridge.stub.DeleteVector(pb.DeleteVectorRequest(id="database", backend="postgres"), timeout=20).success, "delete failed")
                ensure(not bridge.stub.DeleteVector(pb.DeleteVectorRequest(id="database", backend="postgres"), timeout=20).success, "missing deletion reported success")
                result = bridge.stub.SearchVectors(pb.SearchVectorsRequest(query=vectors["query"], top_k=2, threshold=-1, backend="postgres"), timeout=20)
                ensure([row.id for row in result.results] == ["cat"], "deleted vector remained searchable")
                return {"new_bridge_pid": bridge.process.pid, "surviving_vectors_before_delete": 2, "after_delete": 1}
            case("fresh_bridge_process_persistence_and_delete", fresh_process)
        finally:
            receipt["workers"].append(bridge.close())
    finally:
        connection = psycopg2.connect(state["dsn"], connect_timeout=10)
        connection.autocommit = True
        with connection.cursor() as cursor:
            cursor.execute(sql.SQL("DROP SCHEMA {} CASCADE").format(sql.Identifier(schema)))
        connection.close()
        receipt["isolated_schema_removed"] = True
        receipt["finished_utc"] = datetime.now(timezone.utc).isoformat()
        receipt["seconds"] = round(time.monotonic() - started, 3)
        completed_names = {row["name"] for row in receipt["cases"]}
        receipt["missing_cases"] = sorted(expected_cases - completed_names)
        receipt["status"] = "passed" if not receipt["missing_cases"] and all(row["status"] == "passed" for row in receipt["cases"]) else "failed"
        Path(args.receipt).write_text(json.dumps(receipt, indent=2) + "\n")
    return 0 if receipt["status"] == "passed" else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--snapshots", default=str(ROOT / ".cache/semantic-model-snapshots.json"))
    parser.add_argument("--state", default=str(ROOT / ".cache/semantic-runtime/state.json"))
    parser.add_argument("--receipt", default=str(ROOT / ".cache/semantic-runtime/acceptance-receipt.json"))
    parser.add_argument("--restart-postgres", action="store_true")
    parser.add_argument("--worker", choices=("cpu", "mps"), help=argparse.SUPPRESS)
    parser.add_argument("--proof", help=argparse.SUPPRESS)
    arguments = parser.parse_args()
    if arguments.worker:
        worker(arguments.worker, arguments.proof)
    else:
        raise SystemExit(main(arguments))
