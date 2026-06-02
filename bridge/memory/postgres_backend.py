"""PostgreSQL/pgvector backend for Overkill's vector memory service.

Lightweight — one connection per operation, no connection pooling needed
for single-user agent. Schema auto-created on first use.

Uses ``DATABASE_URL`` or ``PG_URL`` env var for the connection string.
Falls back to ``dbname=overkill`` (local socket) when unset.
"""

from __future__ import annotations

import json
import logging
import os

logger = logging.getLogger(__name__)

_INDEX_MIN_ROWS = 1000
_schema_ensured = False


def _dsn() -> str:
    return os.environ.get(
        "DATABASE_URL",
        os.environ.get("PG_URL", "dbname=overkill"),
    )


def _get_conn():
    """Return a new psycopg2 connection. Lightweight — one per op."""
    import psycopg2
    dsn = _dsn()
    conn = psycopg2.connect(dsn)
    conn.autocommit = True
    _ensure_schema(conn)
    return conn


def _ensure_schema(conn) -> None:
    global _schema_ensured
    if _schema_ensured:
        return
    cur = conn.cursor()
    try:
        cur.execute("CREATE EXTENSION IF NOT EXISTS vector")
        cur.execute(
            """
            CREATE TABLE IF NOT EXISTS vector_memories (
                id          TEXT PRIMARY KEY,
                embedding   vector,
                content     TEXT NOT NULL,
                metadata    JSONB DEFAULT '{}',
                created_at  TIMESTAMPTZ DEFAULT NOW()
            )
            """
        )
        from pgvector.psycopg2 import register_vector
        register_vector(conn)
        _schema_ensured = True
        logger.info("pgvector schema ready")
    finally:
        cur.close()


def store(
    entry_id: str,
    embedding: list[float],
    content: str,
    metadata: dict[str, str] | None = None,
) -> str:
    conn = _get_conn()
    meta_json = json.dumps(metadata or {}, default=str)
    vec_str = _format_vector(embedding)
    cur = conn.cursor()
    try:
        cur.execute(
            """
            INSERT INTO vector_memories (id, embedding, content, metadata)
            VALUES (%s, %s::vector, %s, %s)
            ON CONFLICT (id) DO UPDATE SET
                embedding = EXCLUDED.embedding,
                content   = EXCLUDED.content,
                metadata  = EXCLUDED.metadata,
                created_at = NOW()
            """,
            (entry_id, vec_str, content, meta_json),
        )
        logger.debug("Stored entry %s (dim=%d)", entry_id, len(embedding))
    finally:
        cur.close()
        conn.close()
    return entry_id


def search(
    query_embedding: list[float],
    top_k: int = 10,
    threshold: float = 0.0,
    filters: dict[str, str] | None = None,
) -> list[dict]:
    conn = _get_conn()
    vec_str = _format_vector(query_embedding)
    cur = conn.cursor()
    results: list[dict] = []
    try:
        if filters:
            conds = []
            extra_params = []
            for key, value in (filters or {}).items():
                conds.append("metadata @> %s::jsonb")
                extra_params.append(json.dumps({key: value}))
            where = " AND " + " AND ".join(conds)
            params = [vec_str, vec_str, threshold] + extra_params + [vec_str, top_k]
            query = f"""
                SELECT id, 1 - (embedding <=> %s::vector) AS score, content, metadata
                FROM vector_memories
                WHERE 1 - (embedding <=> %s::vector) >= %s{where}
                ORDER BY embedding <=> %s::vector
                LIMIT %s
            """
            cur.execute(query, params)
        else:
            cur.execute(
                """
                SELECT id, 1 - (embedding <=> %s::vector) AS score, content, metadata
                FROM vector_memories
                WHERE 1 - (embedding <=> %s::vector) >= %s
                ORDER BY embedding <=> %s::vector
                LIMIT %s
                """,
                (vec_str, vec_str, threshold, vec_str, top_k),
            )
        for row in cur.fetchall():
            results.append({
                "id": row[0],
                "score": float(row[1]),
                "content": row[2],
                "metadata": row[3] if isinstance(row[3], dict) else {},
            })
    finally:
        cur.close()
        conn.close()
    return results


def delete(entry_id: str) -> bool:
    conn = _get_conn()
    cur = conn.cursor()
    try:
        cur.execute("DELETE FROM vector_memories WHERE id = %s", (entry_id,))
        deleted = cur.rowcount > 0
        return deleted
    finally:
        cur.close()
        conn.close()


def count() -> int:
    conn = _get_conn()
    cur = conn.cursor()
    try:
        cur.execute("SELECT COUNT(*) FROM vector_memories")
        return cur.fetchone()[0]
    finally:
        cur.close()
        conn.close()


def _format_vector(vec: list[float]) -> str:
    return "[" + ",".join(str(v) for v in vec) + "]"


class PostgresVectorBackend:
    """Compatibility wrapper — delegates to module-level functions."""

    def store(self, entry_id, embedding, content, metadata=None):
        return store(entry_id, embedding, content, metadata)

    def search(self, query_embedding, top_k=10, threshold=0.0, filters=None):
        return search(query_embedding, top_k, threshold, filters)

    def delete(self, entry_id):
        return delete(entry_id)

    def count(self):
        return count()

    def close(self):
        pass
