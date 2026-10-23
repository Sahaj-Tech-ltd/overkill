# Python bridge

Python 3.11+ is required. Install the core local gRPC server with
`pip install ./bridge`; this includes generated protocol modules, health
checking, NumPy, PostgreSQL, and pgvector drivers. For real embeddings and
reranking, install `pip install './bridge[ml]'`. Semantic calls fail with gRPC
`UNAVAILABLE` and installation guidance when the model runtime or requested
model cannot load. No model downloads happen during installation or Ping.

Run `overkill-bridge` (or `python -m server` after installation). The server
binds `127.0.0.1:50051`; `--port` and loopback `--host` are configurable.
Wildcard, LAN, and remote host addresses are rejected because this endpoint
has no authentication/TLS. Remote access requires an independently configured
authenticated TLS proxy, with the bridge itself remaining on loopback.

Vector memory defaults to PostgreSQL. Set `DATABASE_URL` (or `PG_URL`) to the
intended database, with permission to create the pgvector extension and tables.
Set `OVERKILL_MEMORY_BACKEND=inmem` explicitly for disposable memory. The server
does not switch silently from persistent to disposable storage.

Vectors must match `OVERKILL_VECTOR_DIMENSION` (default 384), contain finite
float32 values, and have nonzero magnitude. Search requires `top_k` from 1 to
1000 and a finite cosine threshold between -1 and 1. A database/schema records
its vector dimension; use a separate schema or migrate stored vectors before
changing embedding dimensions. Configure the same dimension for all callers.

`Ping` checks server liveness. Standard gRPC health `Check` supports
`embeddings` and `reranking`; readiness is `NOT_SERVING` until that capability
successfully loads a model. An empty service name requires both capabilities
to be ready. A failed model load marks its capability unavailable and remains
retryable; a successful later request restores readiness.

`OVERKILL_ALLOW_MOCK_MODELS=1` explicitly enables deterministic test embeddings
and lexical reranking. These are not semantic models and health remains
`NOT_SERVING` in this mode. Never enable it for production semantic search.

For development, install `pip install './bridge[dev]'` and run
`python -m pytest bridge/tests --timeout=30`. Unit and gRPC tests use controlled
stub models or explicit mocks without downloading ML models. Real pgvector
tests use `OVERKILL_VECTOR_TEST_URL`; set `OVERKILL_REQUIRE_VECTOR_DB=1` to fail
if that URL is absent. Configured unreachable databases always fail tests.
`python scripts/verify_bridge_wheel.py` verifies a built wheel in a clean venv
outside the checkout, including its installed console server.
