package dialog

import "strings"

// MaskKey returns s with all but the last `visible` characters replaced by
// asterisks. Empty string returns empty. visible<=0 fully masks. If the
// string is shorter than visible, the entire string is shown unmasked.
//
// Used to display API keys without leaking them, while letting the user see
// enough of the tail to verify what they typed.
func MaskKey(s string, visible int) string {
	if s == "" {
		return ""
	}
	if visible <= 0 {
		return strings.Repeat("*", len(s))
	}
	if len(s) <= visible {
		return s
	}
	return strings.Repeat("*", len(s)-visible) + s[len(s)-visible:]
}
