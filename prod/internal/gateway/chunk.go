// Package gateway — shared helpers for gateway implementations.
package gateway

// ChunkAtRune splits s at a rune boundary at or before max bytes,
// preferring the last newline within a 200-byte look-behind window so
// we don't cut mid-paragraph. If len(s) <= max, returns (s, "").
//
// Unicode safety: the fallback walks backward through continuation bytes
// (0x80-0xBF, identified by &0xC0==0x80) to find the start of a multi-byte
// UTF-8 rune, ensuring both returned strings are valid UTF-8.
func ChunkAtRune(s string, max int) (head, tail string) {
	if len(s) <= max {
		return s, ""
	}
	// Walk back from max to find a safe break (preferring newline).
	cut := max
	for cut > 0 && cut > max-200 {
		if s[cut] == '\n' {
			return s[:cut], s[cut+1:]
		}
		cut--
	}
	// Fallback: respect rune boundary at max.
	cut = max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut], s[cut:]
}
