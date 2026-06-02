"""Embedding service using sentence-transformers.

Generates dense vector embeddings for text. Auto-detects GPU (CUDA/MPS)
and falls back to CPU. Falls back to deterministic hash-based mock embeddings
when sentence-transformers is not installed.

The mock path is intentionally DETERMINISTIC (SHA-256 hash seed → xorshift)
so that identical texts produce identical mock embeddings. This means the
mock path is functional for deduplication, just not semantically meaningful.
"""

from __future__ import annotations

import hashlib
import logging
import math
import threading
from typing import Any

logger = logging.getLogger(__name__)


class EmbeddingService:
    DEFAULT_MODEL = "all-MiniLM-L6-v2"

    def __init__(self) -> None:
        self._model: Any = None
        self._dimension = 384
        self._initialized = False
        self._loaded_model_name: str = ""
        self._model_lock = threading.Lock()
        self._device: str = ""

    def _detect_device(self) -> str:
        """Detect best available device: cuda > mps > cpu."""
        try:
            import torch  # noqa: F811
            if torch.cuda.is_available():
                return "cuda"
            if hasattr(torch.backends, "mps") and torch.backends.mps.is_available():
                return "mps"
        except ImportError:
            pass
        return "cpu"

    def _ensure_model(self, model: str = "") -> None:
        name = model.strip() if model else self.DEFAULT_MODEL
        # Fast path: already loaded the right model — no lock needed.
        if self._initialized and self._loaded_model_name == name:
            return
        with self._model_lock:
            # Double-check under lock to avoid multiple loads.
            if self._initialized and self._loaded_model_name == name:
                return
            self._initialized = True
            self._loaded_model_name = name
            device = self._detect_device()
            self._device = device
            try:
                from sentence_transformers import SentenceTransformer

                logger.info("Loading embedding model %s on %s", name, device)
                self._model = SentenceTransformer(name, device=device)
                self._dimension = self._model.get_sentence_embedding_dimension() or 384
                logger.info(
                    "Loaded embedding model: %s (dim=%d, device=%s)",
                    name,
                    self._dimension,
                    device,
                )
            except ImportError as exc:
                logger.error(
                    "sentence-transformers not installed — falling back to mock embeddings. "
                    "Install with: pip install sentence-transformers. Error: %s",
                    exc,
                )
                self._model = None

    @property
    def dimension(self) -> int:
        """Current embedding dimension. Call after first embed() to ensure model is loaded."""
        self._ensure_model()
        return self._dimension

    def embed(self, text: str, model: str = "") -> tuple[list[float], int]:
        self._ensure_model(model)
        tokens = max(1, len(text.split()))
        if self._model is not None:
            result = self._model.encode(text, normalize_embeddings=True)
            return result.tolist(), tokens
        embedding = self._mock_embedding(text)
        return embedding, tokens

    def embed_batch(self, texts: list[str], model: str = "") -> list[tuple[list[float], int]]:
        self._ensure_model(model)
        if self._model is not None:
            results = self._model.encode(texts, normalize_embeddings=True)
            return [(r.tolist(), max(1, len(t.split()))) for r, t in zip(results, texts, strict=True)]
        return [(self._mock_embedding(t), max(1, len(t.split()))) for t in texts]

    def _mock_embedding(self, text: str) -> list[float]:
        # Deterministic hash-based mock when sentence-transformers is unavailable.
        # See module docstring for rationale.
        h = hashlib.sha256(text.encode())
        seed = int.from_bytes(h.digest()[:8], "big")
        # Simple xorshift to generate pseudo-random floats from seed.
        vec = []
        state = seed
        for _ in range(self._dimension):
            state ^= state << 13
            state ^= state >> 7
            state ^= state << 17
            vec.append((state % 1000000) / 1000000.0 * 2.0 - 1.0)
        norm = math.sqrt(sum(v * v for v in vec))
        if norm == 0:
            return [1.0 / math.sqrt(self._dimension)] * self._dimension
        return [v / norm for v in vec]
