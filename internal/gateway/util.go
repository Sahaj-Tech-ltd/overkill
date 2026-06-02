// Package gateway — shared helpers for gateway implementations.
package gateway

// TruncateMessage truncates msg to maxLen bytes, preferring clean
// breakpoints: first a newline, then a sentence boundary (". "),
// then a hard byte cut (respecting UTF-8 continuation bytes).
// Appends "…" when truncation occurs.
func TruncateMessage(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}

	// Search within the last 10% of the limit for a clean breakpoint.
	searchWindow := maxLen / 10
	if searchWindow < 1 {
		searchWindow = 1
	}
	searchStart := maxLen - searchWindow
	if searchStart < 0 {
		searchStart = 0
	}

	// Prefer newline boundary.
	for i := maxLen; i > searchStart; i-- {
		if msg[i-1] == '\n' {
			return msg[:i-1] + "…"
		}
	}

	// Fall back to sentence break (". ").
	for i := maxLen; i > searchStart; i-- {
		if i >= 2 && msg[i-2] == '.' && msg[i-1] == ' ' {
			return msg[:i] + "…"
		}
	}

	// Hard truncate at maxLen, respecting UTF-8 byte boundaries.
	cut := maxLen
	for cut > 0 && (msg[cut]&0xC0) == 0x80 {
		cut--
	}
	return msg[:cut] + "…"
}
