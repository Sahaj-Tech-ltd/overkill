package subagent

import (
	"context"
	"strings"
	"testing"
	"time"
)

func validContract() *Contract {
	return &Contract{
		ID:    "c1",
		Goal:  "do the thing",
		Scope: []string{"internal/foo"},
		ExpectedOutputs: []Output{
			{Kind: OutFile, Spec: "internal/foo/bar.go"},
		},
		Acceptance:    []AcceptanceCheck{},
		OnContextFull: OnContextFullCompactAndContinue,
	}
}

func TestContract_Validate_OK(t *testing.T) {
	if err := validContract().Validate(); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestContract_Validate_Errors(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Contract)
		want string
	}{
		{"nil-goal", func(c *Contract) { c.Goal = "" }, "goal is required"},
		{"no-id", func(c *Contract) { c.ID = "" }, "id is required"},
		{"no-outputs", func(c *Contract) { c.ExpectedOutputs = nil }, "expected output is required"},
		{"empty-spec", func(c *Contract) { c.ExpectedOutputs[0].Spec = "" }, "spec is empty"},
		{"bad-kind", func(c *Contract) { c.ExpectedOutputs[0].Kind = "bogus" }, "kind invalid"},
		{"scope-overlap", func(c *Contract) {
			c.Scope = []string{"foo.go"}
			c.OutOfScope = []string{"*.go"}
		}, "overlaps out_of_scope"},
		{"non-file-without-acceptance", func(c *Contract) {
			c.ExpectedOutputs = []Output{{Kind: OutBehavior, Spec: "behaves correctly"}}
		}, "no way to verify"},
		{"bad-on-context", func(c *Contract) { c.OnContextFull = "bogus" }, "invalid on_context_full"},
		{"negative-budget", func(c *Contract) { c.Budget.Steps = -1 }, "must be >= 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validContract()
			tc.mut(c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestContract_CheckScope(t *testing.T) {
	c := &Contract{
		Scope:      []string{"internal/foo", "pkg/bar/**"},
		OutOfScope: []string{"internal/foo/forbidden.go", "**/.env"},
	}
	cases := []struct {
		path    string
		allowed bool
	}{
		{"internal/foo/x.go", true},
		{"internal/foo/sub/y.go", true},
		{"pkg/bar/baz.go", true},
		{"pkg/bar/deep/nested.go", true},
		{"internal/other/z.go", false},
		{"internal/foo/forbidden.go", false},
	}
	for _, tc := range cases {
		got, why := c.CheckScope(tc.path)
		if got != tc.allowed {
			t.Errorf("CheckScope(%q) = %v (%s); want %v", tc.path, got, why, tc.allowed)
		}
	}
}

func TestContract_CheckScope_Empty(t *testing.T) {
	c := &Contract{}
	if ok, _ := c.CheckScope("anywhere/at/all.go"); !ok {
		t.Fatal("empty scope should allow all")
	}
}

func TestContract_RunAcceptance(t *testing.T) {
	c := &Contract{
		Acceptance: []AcceptanceCheck{
			{Name: "pass", Cmd: "true", ExpectExit: 0},
			{Name: "fail-exit", Cmd: "exit 7", ExpectExit: 0},
			{Name: "stdout-ok", Cmd: "echo hello world", ExpectExit: 0, ExpectStdoutContains: "world"},
			{Name: "stdout-miss", Cmd: "echo hello world", ExpectExit: 0, ExpectStdoutContains: "missing"},
		},
	}
	results := c.RunAcceptance(context.Background(), "", nil)
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
	wantPass := []bool{true, false, true, false}
	for i, r := range results {
		if r.Passed != wantPass[i] {
			t.Errorf("results[%d] passed=%v want %v (reason=%s)", i, r.Passed, wantPass[i], r.Reason)
		}
	}
}

func TestContract_RunAcceptance_FakeRunner(t *testing.T) {
	c := &Contract{Acceptance: []AcceptanceCheck{{Name: "x", Cmd: "noop", ExpectExit: 0}}}
	called := false
	fake := func(ctx context.Context, cmd string, to time.Duration, wd string) (int, string, string, error) {
		called = true
		return 0, "", "", nil
	}
	c.RunAcceptance(context.Background(), "", fake)
	if !called {
		t.Fatal("expected custom runner to be invoked")
	}
}

func TestContractViolation_Error(t *testing.T) {
	v := ContractViolation{Criterion: "scope", Reason: "outside"}
	if !strings.Contains(v.Error(), "contract violation: scope") {
		t.Fatalf("unexpected error string: %s", v.Error())
	}
}

// TestContainsShellMetachar_RedirectOperators proves bug #23:
// < and > shell redirect operators are NOT blocked by containsShellMetachar,
// allowing a malicious subagent to redirect I/O.
func TestContainsShellMetachar_RedirectOperators(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"cat < /etc/passwd", true},     // input redirect — SHOULD be blocked
		{"echo data > /tmp/evil", true}, // output redirect — SHOULD be blocked
		{"cmd < file > out", true},      // both redirects
		{"echo hello", false},           // safe command
		{"ls -la", false},               // safe command
		{"cat /etc/hosts", false},       // safe command
	}

	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			got := containsShellMetachar(tc.cmd)
			if got != tc.want {
				t.Errorf("containsShellMetachar(%q) = %v, want %v — BUG #23: < and > not blocked", tc.cmd, got, tc.want)
			}
		})
	}
}
