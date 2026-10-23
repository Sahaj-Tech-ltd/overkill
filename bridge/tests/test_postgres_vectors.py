"""Real pgvector tests; mandatory in the dedicated CI vector job."""
import os
import uuid
from concurrent.futures import ThreadPoolExecutor

import pytest

from memory.service import VectorMemoryService


@pytest.fixture
def database(monkeypatch):
    dsn = os.environ.get("OVERKILL_VECTOR_TEST_URL")
    required = os.environ.get("OVERKILL_REQUIRE_VECTOR_DB") == "1"
    if not dsn:
        if required:
            pytest.fail("OVERKILL_VECTOR_TEST_URL is required for pgvector integration")
        pytest.skip("Set OVERKILL_VECTOR_TEST_URL to run real pgvector integration")
    # A configured but unreachable service is always a failure.
    import psycopg2
    from psycopg2 import sql
    from psycopg2.extensions import make_dsn
    schema = 'vector_test_' + uuid.uuid4().hex
    with psycopg2.connect(dsn, connect_timeout=5) as conn, conn.cursor() as cur:
        cur.execute("CREATE EXTENSION IF NOT EXISTS vector SCHEMA public")
        cur.execute(sql.SQL("CREATE SCHEMA {}").format(sql.Identifier(schema)))
    monkeypatch.setenv("DATABASE_URL", make_dsn(dsn, options=f"-c search_path={schema},public", connect_timeout=5))
    try:
        yield
    finally:
        with psycopg2.connect(dsn, connect_timeout=5) as conn, conn.cursor() as cur:
            cur.execute(sql.SQL("DROP SCHEMA {} CASCADE").format(sql.Identifier(schema)))


def test_backend_parity(database):
    memory = VectorMemoryService(dimension=3)
    for backend in ('inmem', 'postgres'):
        assert memory.store('a', [1, 2, 3], 'original', {'kind': 'a'}, backend=backend) == 'a'
        memory.store('b', [3, 2, 1], 'second', {'kind': 'b'}, backend=backend)
        memory.store('a', [1, 2, 3], 'updated', {'kind': 'a'}, backend=backend)
        results = memory.search([1, 2, 3], top_k=1, filters={'kind': 'a'}, backend=backend)
        assert len(results) == 1
        assert results[0]['id'] == 'a'
        assert results[0]['content'] == 'updated'
        assert results[0]['score'] == pytest.approx(1, abs=1e-6)
        for vector in ([1], [1, float('nan'), 2], [0, 0, 0]):
            with pytest.raises(ValueError):
                memory.store('bad', vector, 'bad', {}, backend=backend)
        with pytest.raises(ValueError, match='top_k'):
            memory.search([1, 2, 3], top_k=-1, backend=backend)
        assert memory.delete('a', backend=backend)
        assert not memory.delete('a', backend=backend)


def test_concurrent_first_use_and_connections(database):
    memory = VectorMemoryService(dimension=3)
    with ThreadPoolExecutor(max_workers=8) as pool:
        list(pool.map(lambda i: memory.store(str(i), [1, 2, 3], str(i), {}, backend='postgres'), range(24)))
        results = list(pool.map(lambda _: memory.search([1, 2, 3], top_k=100, backend='postgres'), range(8)))
    assert all(len(batch) == 24 for batch in results)


def test_persistent_dimension_contract(database):
    original = VectorMemoryService(dimension=3)
    original.store('a', [1, 2, 3], 'original', {}, backend='postgres')
    incompatible = VectorMemoryService(dimension=2)
    with pytest.raises(ValueError, match='Database vector dimension is 3'):
        incompatible.store('b', [1, 2], 'wrong dimension', {}, backend='postgres')
    assert original.search([1, 2, 3], top_k=1, backend='postgres')[0]['id'] == 'a'
