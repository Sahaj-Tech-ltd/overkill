import math
import threading


class VectorMemoryService:
    def __init__(self) -> None:
        self._store: dict[str, dict] = {}
        self._mu = threading.Lock()

    def store(
        self, entry_id: str, embedding: list[float], content: str, metadata: dict[str, str], backend: str = "inmem"
    ) -> str:
        _ = backend  # reserved for Postgres/Qdrant disk backends; inmem only for now
        with self._mu:
            self._store[entry_id] = {
                "id": entry_id,
                "embedding": embedding,
                "content": content,
                "metadata": metadata,
            }
        return entry_id

    def search(
        self,
        query_embedding: list[float],
        top_k: int = 10,
        threshold: float = 0.0,
        filters: dict[str, str] | None = None,
        backend: str = "inmem",
    ) -> list[dict]:
        _ = backend  # reserved for Postgres/Qdrant disk backends; inmem only for now
        results: list[dict] = []
        # B016: Copy entries under lock, then compute cosine outside lock
        # to avoid holding the mutex during O(n) vector operations.
        with self._mu:
            entries = list(self._store.values())
        for entry in entries:
            if filters and not self._matches_filters(entry.get("metadata", {}), filters):
                continue
            score = self._cosine_similarity(query_embedding, entry["embedding"])
            if score >= threshold:
                results.append(
                    {
                        "id": entry["id"],
                        "score": score,
                        "content": entry["content"],
                        "metadata": entry.get("metadata", {}),
                    }
                )
        results.sort(key=lambda x: x["score"], reverse=True)
        return results[:top_k]

    def delete(self, entry_id: str, backend: str = "inmem") -> bool:
        _ = backend  # reserved for Postgres/Qdrant disk backends; inmem only for now
        with self._mu:
            if entry_id in self._store:
                del self._store[entry_id]
                return True
            return False

    def _matches_filters(self, metadata: dict, filters: dict[str, str]) -> bool:
        return all(metadata.get(key) == value for key, value in filters.items())

    @staticmethod
    def _cosine_similarity(a: list[float], b: list[float]) -> float:
        dot = sum(x * y for x, y in zip(a, b, strict=False))
        norm_a = math.sqrt(sum(x * x for x in a))
        norm_b = math.sqrt(sum(x * x for x in b))
        if norm_a == 0 or norm_b == 0:
            return 0.0
        return dot / (norm_a * norm_b)
