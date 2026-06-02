from __future__ import annotations

import logging
import math
import threading
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .postgres_backend import PostgresVectorBackend

logger = logging.getLogger(__name__)

_SUPPORTED_BACKENDS = frozenset({"inmem", "postgres"})


class VectorMemoryService:
    _MAX_ENTRIES = 10_000

    def __init__(self) -> None:
        self._store: dict[str, dict] = {}
        self._mu = threading.RLock()
        self._store_order: list[str] = []
        self._pg_backend: PostgresVectorBackend | None = None
        self._pg_lock = threading.Lock()

    def _validate_backend(self, backend: str) -> None:
        if backend not in _SUPPORTED_BACKENDS:
            supported = ", ".join(sorted(_SUPPORTED_BACKENDS))
            raise ValueError(f"Unsupported backend '{backend}'. Supported: {supported}")

    def _get_pg(self) -> PostgresVectorBackend:
        if self._pg_backend is not None:
            return self._pg_backend
        with self._pg_lock:
            if self._pg_backend is not None:
                return self._pg_backend
            from .postgres_backend import PostgresVectorBackend
            self._pg_backend = PostgresVectorBackend()
            return self._pg_backend

    # ── Public API ─────────────────────────────────────────────────────

    def store(
        self, entry_id: str, embedding: list[float], content: str,
        metadata: dict[str, str], backend: str = "postgres",
    ) -> str:
        self._validate_backend(backend)
        if backend == "postgres":
            return self._get_pg().store(entry_id, embedding, content, metadata)
        with self._mu:
            if entry_id not in self._store and len(self._store) >= self._MAX_ENTRIES:
                oldest = self._store_order.pop(0)
                self._store.pop(oldest, None)
                logger.warning("Evicted oldest entry %s (store at capacity %d)", oldest, self._MAX_ENTRIES)
            self._store[entry_id] = {
                "id": entry_id, "embedding": embedding,
                "content": content, "metadata": metadata,
            }
            if entry_id not in self._store_order:
                self._store_order.append(entry_id)
        logger.debug("Stored entry %s (backend=%s)", entry_id, backend)
        return entry_id

    def search(
        self, query_embedding: list[float], top_k: int = 10,
        threshold: float = 0.0, filters: dict[str, str] | None = None,
        backend: str = "postgres",
    ) -> list[dict]:
        self._validate_backend(backend)
        if backend == "postgres":
            return self._get_pg().search(query_embedding, top_k, threshold, filters)
        results: list[dict] = []
        with self._mu:
            entries = list(self._store.values())
        for entry in entries:
            if filters and not self._matches_filters(entry.get("metadata", {}), filters):
                continue
            score = self._cosine_similarity(query_embedding, entry["embedding"])
            if score >= threshold:
                results.append({
                    "id": entry["id"], "score": score,
                    "content": entry["content"],
                    "metadata": entry.get("metadata", {}),
                })
        results.sort(key=lambda x: x["score"], reverse=True)
        return results[:top_k]

    def delete(self, entry_id: str, backend: str = "postgres") -> bool:
        self._validate_backend(backend)
        if backend == "postgres":
            return self._get_pg().delete(entry_id)
        with self._mu:
            if entry_id in self._store:
                del self._store[entry_id]
                if entry_id in self._store_order:
                    self._store_order.remove(entry_id)
                return True
            return False

    @staticmethod
    def _matches_filters(metadata: dict, filters: dict[str, str]) -> bool:
        return all(metadata.get(key) == value for key, value in filters.items())

    @staticmethod
    def _cosine_similarity(a: list[float], b: list[float]) -> float:
        if len(a) != len(b):
            raise ValueError(f"Dimension mismatch: {len(a)} vs {len(b)}")
        dot = sum(x * y for x, y in zip(a, b, strict=False))
        norm_a = math.sqrt(sum(x * x for x in a))
        norm_b = math.sqrt(sum(x * x for x in b))
        if norm_a == 0 or norm_b == 0:
            return 0.0
        return dot / (norm_a * norm_b)
