"""Reranking service using sentence-transformers CrossEncoder.

Re-ranks document lists by relevance to a query. Auto-detects GPU (CUDA/MPS)
and falls back to CPU. Thread-safe model loading (double-checked locking).
Falls back to TF-IDF-inspired mock reranking when sentence-transformers is
not installed.
"""

from __future__ import annotations

import logging
import math
import re
import threading
from typing import Any

logger = logging.getLogger(__name__)


class RerankingService:
    DEFAULT_MODEL = "cross-encoder/ms-marco-MiniLM-L-6-v2"

    def __init__(self) -> None:
        self._model: Any = None
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
        # Fast path — no lock.
        if self._initialized and self._loaded_model_name == name:
            return
        with self._model_lock:
            # Double-check under lock.
            if self._initialized and self._loaded_model_name == name:
                return
            self._initialized = True
            self._loaded_model_name = name
            device = self._detect_device()
            self._device = device
            try:
                from sentence_transformers import CrossEncoder

                logger.info("Loading reranking model %s on %s", name, device)
                self._model = CrossEncoder(name, device=device)
                logger.info("Loaded reranking model: %s (device=%s)", name, device)
            except ImportError as exc:
                logger.error(
                    "sentence-transformers not installed — falling back to mock reranking. "
                    "Install with: pip install sentence-transformers. Error: %s",
                    exc,
                )
                self._model = None

    def rerank(
        self, query: str, documents: list[str], top_n: int = 0, model: str = ""
    ) -> list[tuple[int, float, str]]:
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
