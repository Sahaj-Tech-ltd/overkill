"""Semantic reranking with serialized model selection and inference.

Mock behavior requires OVERKILL_ALLOW_MOCK_MODELS=1 or allow_mock=True.
"""

from __future__ import annotations

import logging
import math
import re
import threading
from typing import Any

from model_runtime import ModelUnavailableError, mock_models_enabled

logger = logging.getLogger(__name__)


class RerankingService:
    DEFAULT_MODEL = "cross-encoder/ms-marco-MiniLM-L-6-v2"

    def __init__(self, *, allow_mock: bool | None = None) -> None:
        self._allow_mock = mock_models_enabled() if allow_mock is None else allow_mock
        self._load_error: str | None = None
        self._model: Any = None
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
                from sentence_transformers import CrossEncoder
                candidate = CrossEncoder(name, device=device)
            except Exception as exc:
                self._load_error = str(exc)
                raise ModelUnavailableError(
                    f"Could not load {name}. Install overkill bridge's [ml] extra "
                    "and ensure the model is available locally or downloadable. "
                    "Mock semantic behavior requires explicit OVERKILL_ALLOW_MOCK_MODELS=1."
                ) from exc
            # Publish the complete replacement only after construction succeeds.
            self._model = candidate
            self._device = device
            self._loaded_model_name = name
            self._initialized = True
            self._load_error = None

    def rerank(
        self, query: str, documents: list[str], top_n: int = 0, model: str = ""
    ) -> list[tuple[int, float, str]]:
        with self._model_lock:
            self._ensure_model(model)
            if top_n <= 0:
                top_n = len(documents)
            if self._model is not None:
                pairs = [(query, doc) for doc in documents]
                # Batch prediction for better throughput on GPU.
                if len(pairs) > 32:
                    scores: list[float] = []
                    for chunk_start in range(0, len(pairs), 32):
                        chunk = pairs[chunk_start : chunk_start + 32]
                        chunk_scores = self._model.predict(chunk)
                        if hasattr(chunk_scores, "tolist"):
                            chunk_scores = chunk_scores.tolist()
                        scores.extend(float(s) for s in chunk_scores)
                else:
                    scores = self._model.predict(pairs)
                    if hasattr(scores, "tolist"):
                        scores = scores.tolist()
                    scores = [float(s) for s in scores]
                indexed = [(i, scores[i], documents[i]) for i in range(len(documents))]
                indexed.sort(key=lambda x: x[1], reverse=True)
                return indexed[:top_n]
            return self._mock_rerank(query, documents, top_n)

    def _mock_rerank(
        self, query: str, documents: list[str], top_n: int
    ) -> list[tuple[int, float, str]]:
        query_terms = set(re.findall(r"\w+", query.lower()))
        scored: list[tuple[int, float, str]] = []
        for i, doc in enumerate(documents):
            doc_terms = set(re.findall(r"\w+", doc.lower()))
            overlap = len(query_terms & doc_terms)
            denom = math.sqrt(len(query_terms)) * math.sqrt(len(doc_terms))
            score = overlap / denom if denom > 0 else 0.0
            scored.append((i, score, doc))
        scored.sort(key=lambda x: x[1], reverse=True)
        return scored[:top_n]
