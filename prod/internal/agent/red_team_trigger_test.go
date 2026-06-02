package agent

import (
	"strings"
	"testing"
)

func TestCriticalityCheck_ReadOnlyToolIsSkipped(t *testing.T) {
	matched, _ := criticalityCheck("fs_read", `{"path":"auth/handlers.go"}`)
	if matched {
		t.Error("read-only tool should never trigger red team")
	}
}

func TestCriticalityCheck_ShellAuthMatches(t *testing.T) {
	matched, reason := criticalityCheck("shell", `{"command":"vim internal/auth/login.go"}`)
	if !matched {
		t.Error("shell touching auth path should match")
	}
	if !strings.Contains(reason, "shell") || !strings.Contains(reason, "auth") {
		t.Errorf("reason missing tool+keyword: %q", reason)
	}
}

func TestCriticalityCheck_PatchPaymentMatches(t *testing.T) {
	matched, _ := criticalityCheck("patch", `{"path":"src/payment_flow.go","patch":"..."}`)
	if !matched {
		t.Error("patch on payment path should match")
	}
}

func TestCriticalityCheck_CryptoPathMatches(t *testing.T) {
	matched, _ := criticalityCheck("fs_write", `{"path":"internal/crypto/keys.go"}`)
	if !matched {
		t.Error("crypto path should match")
	}
}

func TestCriticalityCheck_BenignPathSkipped(t *testing.T) {
	matched, _ := criticalityCheck("patch", `{"path":"docs/README.md"}`)
	if matched {
		t.Error("docs path should not match red-team criticality")
	}
}

func TestCriticalityCheck_EmptyArgsSkipped(t *testing.T) {
	matched, _ := criticalityCheck("shell", "")
	if matched {
		t.Error("empty args should skip cheaply")
	}
}

func TestPreToolRedTeamCheck_NoEmitForBenignCall(t *testing.T) {
	a := &Agent{_sessionID: "s1"}
	events := captureEmits(a)
	a.preToolRedTeamCheck("fs_read", []byte(`{"path":"auth/x.go"}`))
	if seen := events.byName("red_team_recommended"); len(seen) != 0 {
		t.Errorf("read-only call should NOT emit; got %d", len(seen))
	}
}

func TestPreToolRedTeamCheck_EmitsForCriticalShell(t *testing.T) {
	a := &Agent{_sessionID: "s1"}
	events := captureEmits(a)
	a.preToolRedTeamCheck("shell", []byte(`{"command":"rm internal/payment/charge.go"}`))
	seen := events.byName("red_team_recommended")
	if len(seen) != 1 {
		t.Fatalf("expected one emit, got %d", len(seen))
	}
	if seen[0]["tool"] != "shell" {
		t.Errorf("event tool field: %v", seen[0]["tool"])
	}
}

// --- tiny event-capture helper ---

type capturedEvents struct {
	events []map[string]any
	names  []string
}

func (c *capturedEvents) byName(name string) []map[string]any {
	var out []map[string]any
	for i, n := range c.names {
		if n == name {
			out = append(out, c.events[i])
		}
	}
	return out
}

func captureEmits(a *Agent) *capturedEvents {
	c := &capturedEvents{}
	a.SetEventFn(func(name string, payload map[string]any) {
		c.names = append(c.names, name)
		c.events = append(c.events, payload)
	})
	return c
}
