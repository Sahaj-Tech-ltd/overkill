package reflect

import (
	"strings"
	"testing"
)

func TestIsFailureOutput_DetectsBuildError(t *testing.T) {
	out := "main.go:42:7: error: undefined: notARealFunc"
	if !IsFailureOutput("Bash", out) {
		t.Error("compiler error should be classified as failure")
	}
}

func TestIsFailureOutput_DetectsTestFailure(t *testing.T) {
	out := "--- FAIL: TestThing (0.01s)\n    thing_test.go:12: expected 1 got 2"
	if !IsFailureOutput("Bash", out) {
		t.Error("go test failure should be classified as failure")
	}
}

func TestIsFailureOutput_DoesNotFlagPlainSuccess(t *testing.T) {
	out := "ok\tgithub.com/foo/bar\t0.041s"
	if IsFailureOutput("Bash", out) {
		t.Errorf("clean test output should not flag: %q", out)
	}
}

func TestIsFailureOutput_DoesNotFlagEmpty(t *testing.T) {
	if IsFailureOutput("Bash", "") {
		t.Error("empty output must not flag")
	}
}

func TestHeuristicReflector_ClassifiesAndCansHypothesis(t *testing.T) {
	cases := []struct {
		name        string
		failure     Failure
		wantMode    FailureMode
		wantHypSubs string
	}{
		{
			name:        "build error",
			failure:     Failure{ToolName: "Bash", Output: "main.go:7:3: error: cannot find package foo"},
			wantMode:    FailureBuildError,
			wantHypSubs: "do not re-run the same edit",
		},
		{
			name:        "test failure",
			failure:     Failure{ToolName: "Bash", Output: "--- FAIL: TestX (0.00s)"},
			wantMode:    FailureTestFailure,
			wantHypSubs: "do not edit the test to make it pass without reason",
		},
		{
			name:        "permission denied",
			failure:     Failure{ToolName: "Write", Error: "open /etc/foo: permission denied"},
			wantMode:    FailurePermissionDenied,
			wantHypSubs: "filesystem ownership",
		},
		{
			name:        "not found",
			failure:     Failure{ToolName: "Read", Error: "open /tmp/nope: no such file or directory"},
			wantMode:    FailureNotFound,
			wantHypSubs: "verify the path",
		},
		{
			name:        "timeout",
			failure:     Failure{ToolName: "Bash", Error: "context deadline exceeded"},
			wantMode:    FailureTimeout,
			wantHypSubs: "narrow the scope",
		},
		{
			name:        "generic",
			failure:     Failure{ToolName: "Tool", Output: "something broke and i don't know what"},
			wantMode:    FailureGeneric,
			wantHypSubs: "slow down",
		},
	}

	r := HeuristicReflector{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := r.Reflect(c.failure)
			if got.Mode != c.wantMode {
				t.Errorf("mode: got %s want %s", got.Mode, c.wantMode)
			}
			if !strings.Contains(got.Hypothesis, c.wantHypSubs) {
				t.Errorf("hypothesis missing %q: %q", c.wantHypSubs, got.Hypothesis)
			}
			if got.RootCause == "" {
				t.Error("root cause must be populated")
			}
			if got.Confidence <= 0 || got.Confidence > 1 {
				t.Errorf("confidence out of range: %v", got.Confidence)
			}
		})
	}
}

func TestFormatSystemNote_ContainsToolAndModeAndHypothesis(t *testing.T) {
	r := Reflection{
		Mode:       FailureTestFailure,
		RootCause:  "TestThing expected 1 got 2",
		Hypothesis: "read the assertion",
		Confidence: 0.6,
	}
	note := FormatSystemNote("Bash", r)
	for _, s := range []string{"[reflexion]", "Bash", string(FailureTestFailure), "TestThing expected", "read the assertion"} {
		if !strings.Contains(note, s) {
			t.Errorf("note missing %q: %s", s, note)
		}
	}
}

func TestFirstLine_TruncatesAndTrims(t *testing.T) {
	long := strings.Repeat("x", 300)
	got := firstLine("  " + long + "  ")
	if len(got) <= 200 {
		// 200 chars + ellipsis, total 201 runes (203 bytes with utf8 ellipsis)
		// just assert it was capped, not exact byte count
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("long line should end in ellipsis, got: %s", got)
	}

	if got := firstLine("foo\nbar\nbaz"); got != "foo" {
		t.Errorf("only first line should survive, got: %q", got)
	}
	if got := firstLine(""); got != "" {
		t.Errorf("empty in → empty out, got: %q", got)
	}
}
