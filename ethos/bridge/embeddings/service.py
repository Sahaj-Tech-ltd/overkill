import math
import random
from typing import Any


class EmbeddingService:
    def __init__(self) -> None:
        self._model: Any = None
        self._dimension = 384
        self._initialized = False

    def _ensure_model(self) -> None:
        if self._initialized:
            return
        self._initialized = True
        try:
            from sentence_transformers import SentenceTransformer

            self._model = SentenceTransformer("all-MiniLM-L6-v2")
            self._dimension = self._model.get_sentence_embedding_dimension() or 384
        except ImportError:
            self._model = None

    def embed(self, text: str, model: str = "") -> tuple[list[float], int]:
        self._ensure_model()
        tokens = max(1, len(text.split()))
        if self._model is not None:
            result = self._model.encode(text, normalize_embeddings=True)
            return result.tolist(), tokens
        embedding = self._mock_embedding()
        return embedding, tokens

    def embed_batch(self, texts: list[str], model: str = "") -> list[tuple[list[float], int]]:
        self._ensure_model()
        if self._model is not None:
            results = self._model.encode(texts, normalize_embeddings=True)
            return [(r.tolist(), max(1, len(t.split()))) for r, t in zip(results, texts, strict=False)]
        return [(self._mock_embedding(), max(1, len(t.split()))) for t in texts]

    def _mock_embedding(self) -> list[float]:
        vec = [random.gauss(0, 1) for _ in range(self._dimension)]
        norm = math.sqrt(sum(v * v for v in vec))
        if norm == 0:
            return [1.0 / math.sqrt(self._dimension)] * self._dimension
        return [v / norm for v in vec]
