package agent

import (
	"strings"
	"testing"
)

func TestLooksLikeLargeTask(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"short and casual", "hi", false},
		{"short and verby", "refactor it", false}, // <80 chars
		{"short with code fence", "```\nprint(1)\n```", true},
		{
			"long verby task",
			"refactor the auth module to use bcrypt and add timing-safe compare for password verification",
			true,
		},
		{
			"long non-verby still triggers via length",
			strings.Repeat("words ", 200),
			true,
		},
		{"non-verb short msg ignored", "explain how globs work in Go", false},
		{
			"implement multiline counts",
			"implement a queue with FIFO drain and double-Esc interrupt that captures the latest queued message and restores it to the editor",
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeLargeTask(tc.input); got != tc.want {
				t.Errorf("looksLikeLargeTask(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestFirstWordLower(t *testing.T) {
	cases := map[string]string{
		"refactor the thing":  "refactor",
		"BUILD me a feature":  "BUILD", // function does NOT lower — caller pre-lowers
		"single":              "single",
		"  leading spaces ok": "", // leading whitespace returns empty first token
	}
	for in, want := range cases {
		got := firstWordLower(in)
		if got != want {
			t.Errorf("firstWordLower(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPreCompactCheck_NoCompactorSkips: without a wired compactor we
// never pre-compact, regardless of utilization or input shape.
func TestPreCompactCheck_NoCompactorSkips(t *testing.T) {
	a := &Agent{}
	// No compactor, no budget estimator — both gate out.
	if a.preCompactCheck(nil, strings.Repeat("x ", 500)) {
		t.Error("missing infrastructure should never pre-compact")
	}
}
