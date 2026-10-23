"""Semantic embeddings with serialized model selection and inference.

Mock behavior requires OVERKILL_ALLOW_MOCK_MODELS=1 or allow_mock=True.
"""

from __future__ import annotations

import hashlib
import logging
import math
import threading
from typing import Any

from model_runtime import ModelUnavailableError, mock_models_enabled

logger = logging.getLogger(__name__)


class EmbeddingService:
    DEFAULT_MODEL = "all-MiniLM-L6-v2"

    def __init__(self, *, allow_mock: bool | None = None) -> None:
        self._allow_mock = mock_models_enabled() if allow_mock is None else allow_mock
        self._load_error: str | None = None
        self._model: Any = None
        self._dimension = 384
        self._initialized = False
        self._loaded_model_name: str = ""
        self._model_lock = threading.RLock()
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

    @property
    def capability_status(self) -> str:
        with self._model_lock:
            if self._allow_mock:
                return "mock"
            if self._load_error:
                return "unavailable"
            return "ready" if self._initialized else "not_loaded"

    def _ensure_model(self, model: str = "") -> None:
        name = model.strip() or self.DEFAULT_MODEL
        with self._model_lock:
            if self._initialized and self._loaded_model_name == name:
                self._load_error = None
                return
            if self._allow_mock:
                self._model = None
                self._loaded_model_name = name
                self._initialized = True
                return
            device = self._detect_device()
            try:
                from sentence_transformers import SentenceTransformer
                candidate = SentenceTransformer(name, device=device)
                dimension = candidate.get_sentence_embedding_dimension()
                if not dimension or dimension <= 0:
                    raise ModelUnavailableError("Embedding model has no valid dimension")
            except Exception as exc:
                self._load_error = str(exc)
                raise ModelUnavailableError(
                    f"Could not load {name}. Install overkill bridge's [ml] extra "
                    "and ensure the model is available locally or downloadable. "
                    "Mock semantic behavior requires explicit OVERKILL_ALLOW_MOCK_MODELS=1."
                ) from exc
            # Publish the complete replacement only after construction succeeds.
            self._model = candidate
            self._dimension = dimension
            self._device = device
            self._loaded_model_name = name
            self._initialized = True
            self._load_error = None

    @property
    def dimension(self) -> int:
        """Current embedding dimension after loading the default model."""
        with self._model_lock:
            self._ensure_model()
            return self._dimension

    def embed(self, text: str, model: str = "") -> tuple[list[float], int]:
        with self._model_lock:
            self._ensure_model(model)
            tokens = max(1, len(text.split()))
            if self._model is not None:
                result = self._model.encode(text, normalize_embeddings=True)
                return result.tolist(), tokens
            embedding = self._mock_embedding(text)
            return embedding, tokens

    def embed_batch(self, texts: list[str], model: str = "") -> list[tuple[list[float], int]]:
        with self._model_lock:
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
