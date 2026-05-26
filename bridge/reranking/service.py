import math
import re


class RerankingService:
    def __init__(self) -> None:
        self._model: object | None = None
        self._initialized = False

    def _ensure_model(self) -> None:
        if self._initialized:
            return
        self._initialized = True
        try:
            from sentence_transformers import CrossEncoder

            self._model = CrossEncoder("cross-encoder/ms-marco-MiniLM-L-6-v2")
        except ImportError:
            self._model = None

    def rerank(
        self, query: str, documents: list[str], top_n: int = 0
    ) -> list[tuple[int, float, str]]:
        self._ensure_model()
        if top_n <= 0:
            top_n = len(documents)
        if self._model is not None:
            pairs = [(query, doc) for doc in documents]
            scores = self._model.predict(pairs)
            indexed = [(i, float(s), documents[i]) for i, s in enumerate(scores)]
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
