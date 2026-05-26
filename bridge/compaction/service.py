class CompactionService:
    def compact(
        self,
        content: str,
        model: str = "",
        target_tokens: int = 500,
        style: str = "detailed",
    ) -> tuple[str, int, int]:
        original_tokens = self._estimate_tokens(content)
        if style == "truncate":
            summary = self._truncate(content, target_tokens)
        elif style == "aggressive":
            summary = self._truncate(content, max(target_tokens // 2, 50))
        else:
            summary = self._truncate(content, target_tokens)
        summary_tokens = self._estimate_tokens(summary)
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
