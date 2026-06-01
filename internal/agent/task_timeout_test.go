package agent

import (
	"context"
	"testing"
	"time"
)

func TestTaskTimeoutFor_Bands(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		history  int
		wantBand time.Duration
	}{
		// Simple band (≤ 60s).
		{"trivial what-question", "what time is it?", 0, 60 * time.Second},
		{"explain short", "explain how globs work", 0, 60 * time.Second},

		// Moderate band — refactor verb (0.20) + 200<len<800 (0.15) + one
		// code block (0.20) = 0.55, falls in [0.30, 0.60] → 5m.
		{
			"long refactor with one code block",
			"refactor the authentication module to use bcrypt and add timing-safe compare. Replace existing md5 calls. Update tests. Here's the relevant snippet:\n```go\nfunc Hash(s string) string { return md5.Sum([]byte(s)) }\n```",
			5,
			5 * time.Minute,
		},

		// Critical — attachments + verb + length.
		{
			"refactor with attachments and long",
			"refactor @auth.go and @users.go and @session.go to consolidate token handling. This is a multi-step task requiring coordinated edits across several files in the package boundary." +
				" Multiple code blocks below:\n```go\nfunc a(){}\n```\n```go\nfunc b(){}\n```",
			20,
			30 * time.Minute,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := taskTimeoutFor(tc.input, tc.history)
			if got != tc.wantBand {
				score := complexityScore(tc.input, tc.history)
				t.Errorf("taskTimeoutFor(...) = %v, want %v (score=%.2f)", got, tc.wantBand, score)
			}
		})
	}
}

func TestComplexityScore_BoundedAndDeterministic(t *testing.T) {
	cases := []string{"", "hi", "explain stuff", "refactor everything"}
	for _, c := range cases {
		s := complexityScore(c, 0)
		if s < 0 || s > 1 {
			t.Errorf("score out of bounds for %q: %.2f", c, s)
		}
		// Determinism — same input must give same score on repeat call.
		if s2 := complexityScore(c, 0); s != s2 {
			t.Errorf("score non-deterministic for %q: %.2f vs %.2f", c, s, s2)
		}
	}
}

func TestWithTaskTimeout_RespectsParentCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx, taskCancel := withTaskTimeout(parent, "what time is it", 0)
	defer taskCancel()
	cancel()
	select {
	case <-ctx.Done():
		// good
	case <-time.After(time.Second):
		t.Error("derived ctx should observe parent cancel immediately")
	}
}

func TestWithTaskTimeout_ExpiresAtBudget(t *testing.T) {
	// Tiny budget via a trivial input. We can't make the budget
	// genuinely tiny without exposing an injection seam — instead we
	// assert that the deadline is set and within the simple band.
	ctx, taskCancel := withTaskTimeout(context.Background(), "what now", 0)
	defer taskCancel()
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected derived ctx to carry a deadline")
	}
	if time.Until(dl) > 65*time.Second {
		t.Errorf("simple-band deadline should be ~60s, got %v", time.Until(dl))
	}
}
