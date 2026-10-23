#!/usr/bin/env python3
"""Build/install the bridge in isolation and run its installed console server.

No ML extra, model download, source-path imports, or editable installation is
used. Runtime dependencies are resolved by pip from the wheel's own metadata.
"""
import os
import shutil
import subprocess
import sys
import tempfile
import venv
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def run(*args, **kwargs):
    return subprocess.run(args, check=True, timeout=180, **kwargs)


def main():
    with tempfile.TemporaryDirectory(prefix="overkill-wheel-") as directory:
        work = Path(directory)
        wheels = work / "wheels"
        source = work / "source"
        shutil.copytree(ROOT / "bridge", source, ignore=shutil.ignore_patterns("build", "*.egg-info", "__pycache__"))
        run(sys.executable, "-m", "pip", "wheel", "--no-deps", str(source), "-w", str(wheels))
        environment = work / "venv"
        venv.EnvBuilder(with_pip=True).create(environment)
        python = environment / "bin" / "python"
        wheel, = wheels.glob("*.whl")
        run(str(python), "-m", "pip", "install", str(wheel), cwd=work)
        probe = work / "probe.py"
        probe.write_text('''
import importlib.util
import pathlib
import socket
import subprocess
import sys
import grpc
import grpc_health
import psycopg2
import pgvector
from grpc_health.v1 import health_pb2, health_pb2_grpc
import server
import overkill_pb2 as pb
import overkill_pb2_grpc as rpc
assert pathlib.Path(server.__file__).is_relative_to(pathlib.Path(sys.prefix))
assert importlib.util.find_spec("sentence_transformers") is None
console = pathlib.Path(sys.executable).with_name("overkill-bridge")
remote = subprocess.run([str(console), "--host", "0.0.0.0"], capture_output=True, text=True, timeout=10)
assert remote.returncode != 0 and "no TLS/authentication" in remote.stderr
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
process = subprocess.Popen([str(console), "--port", str(port)], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
try:
    with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
        grpc.channel_ready_future(channel).result(timeout=10)
        client = rpc.OverkillBridgeStub(channel)
        assert client.Ping(pb.PingRequest(), timeout=3).status == "ok"
        health = health_pb2_grpc.HealthStub(channel)
        assert health.Check(health_pb2.HealthCheckRequest(service="embeddings"), timeout=3).status == health_pb2.HealthCheckResponse.NOT_SERVING
        try:
            client.Embed(pb.EmbedRequest(text="production must not silently mock"), timeout=3)
            raise AssertionError("missing ML runtime returned successful embeddings")
        except grpc.RpcError as exc:
            assert exc.code() == grpc.StatusCode.UNAVAILABLE
            assert "[ml] extra" in exc.details()
finally:
    process.terminate()
    output, errors = process.communicate(timeout=10)
    assert process.returncode == 0, errors.decode()
print("Installed wheel: imports, loopback console startup, ping, health, unavailable ML, and graceful shutdown PASS")
''')
        env = dict(os.environ)
        env.pop("PYTHONPATH", None)
        env.pop("OVERKILL_ALLOW_MOCK_MODELS", None)
        run(str(python), str(probe), cwd=work, env=env)


if __name__ == "__main__":
    main()
