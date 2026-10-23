"""Context compaction service — deterministic word-truncation fallback.

**Note:** The Go-side LCM compactor (``internal/compaction/lcm.go``) handles
LLM-based semantic compaction with three-level escalation (detailed →
aggressive → truncate). It calls the LLM provider directly — NOT this service.

This Python service exists as a deterministic fallback for non-Go callers or
for testing. The ``model`` parameter is accepted for API compatibility but
has no effect.

**Architecture:** If you're looking for the REAL compaction logic, see
the Go LCM compactor at ``internal/compaction/lcm.go``.
"""

import logging

logger = logging.getLogger(__name__)


class CompactionService:
    def compact(
        self,
        content: str,
        model: str = "",
        target_tokens: int = 500,
        style: str = "detailed",
    ) -> tuple[str, int, int]:
        _ = model  # reserved — Go side handles LLM compaction
        original_tokens = self._estimate_tokens(content)
        if style == "truncate":
            summary = self._truncate(content, target_tokens)
        elif style == "aggressive":
            summary = self._truncate(content, max(target_tokens // 2, 50))
        else:
            summary = self._truncate(content, target_tokens)
        summary_tokens = self._estimate_tokens(summary)
        logger.debug(
            "Compacted: %d → %d tokens (style=%s, deterministic truncation)",
            original_tokens,
            summary_tokens,
            style,
        )
        return summary, original_tokens, summary_tokens

    @staticmethod
    def _estimate_tokens(text: str) -> int:
        return max(1, int(len(text.split()) * 1.3))

    @staticmethod
    def _truncate(text: str, target_tokens: int) -> str:
        words = text.split()
        target_words = max(1, int(target_tokens / 1.3))
        if len(words) <= target_words:
            return text
        return " ".join(words[:target_words])
