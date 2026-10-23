package security

import (
	"encoding/json"
	"testing"
)

func TestPrivilegeGate_DefaultsToWriter(t *testing.T) {
	g := NewPrivilegeGate("")
	if g.Mode() != ModeWriter {
		t.Fatalf("default mode = %s want writer", g.Mode())
	}
	ok, _ := g.Allow("fs_write", json.RawMessage(`{}`))
	if !ok {
		t.Fatal("writer should allow writes")
	}
}

func TestPrivilegeGate_ReaderBlocksWriteLike(t *testing.T) {
	g := NewPrivilegeGate(ModeReader)
	cases := []struct {
		name string
		in   string
		want bool // false = blocked
	}{
		{"fs_write", `{"path":"x"}`, false},
		{"patch", `{}`, false},
		{"shell-rm", ``, false},
		{"git-push", ``, false},
		{"fs-read", `{"action":"read","path":"x"}`, true},
		{"shell-echo", ``, false},
		{"grep", `{}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolName, raw := tc.name, json.RawMessage(tc.in)
			switch tc.name {
			case "shell-rm":
				toolName = "shell"
				raw = json.RawMessage(`{"command":"rm /tmp/x"}`)
			case "git-push":
				toolName = "git"
				raw = json.RawMessage(`{"subcommand":"push"}`)
			case "fs-read":
				toolName = "fs"
			case "shell-echo":
				toolName = "shell"
				raw = json.RawMessage(`{"command":"echo hi"}`)
			}
			ok, why := g.Allow(toolName, raw)
			if ok != tc.want {
				t.Fatalf("Allow(%s)=%v want %v (why=%s)", toolName, ok, tc.want, why)
			}
		})
	}
}

func TestPrivilegeGate_SetMode(t *testing.T) {
	g := NewPrivilegeGate(ModeWriter)
	prev := g.SetMode(ModeReader)
	if prev != ModeWriter || g.Mode() != ModeReader {
		t.Fatalf("transition broken: prev=%s now=%s", prev, g.Mode())
	}
	g.SetMode("invalid")
	if g.Mode() != ModeReader {
		t.Fatal("invalid mode should not change current")
	}
}

func TestIsWriteLikeTool_FsActionBranches(t *testing.T) {
	cases := []struct {
		action string
		want   bool
	}{
		{"read", false},
		{"write", true},
		{"create", true},
		{"delete", true},
		{"unknown", true},
	}
	for _, tc := range cases {
		raw, _ := json.Marshal(map[string]string{"action": tc.action})
		if got := IsWriteLikeTool("fs", raw); got != tc.want {
			t.Errorf("fs/%s got=%v want=%v", tc.action, got, tc.want)
		}
	}
}

func TestPrivilegeDurableExternalTaskMutations(t *testing.T) {
	gate := NewPrivilegeGate(ModeReader)
	for _, name := range []string{"acp_submit", "acp_cancel", "acp_send"} {
		if ok, _ := gate.Allow(name, json.RawMessage(`{}`)); ok {
			t.Fatalf("reader admitted external mutation %s", name)
		}
	}
	for _, name := range []string{"acp_status", "acp_await", "acp_sessions"} {
		if ok, _ := gate.Allow(name, json.RawMessage(`{}`)); !ok {
			t.Fatalf("reader blocked observation %s", name)
		}
	}
}

func TestReaderRequiresExplicitObservation(t *testing.T) {
	gate := NewPrivilegeGate(ModeReader)
	for _, name := range []string{"unclassified_extension", "mcp_custom_tool", "delegate_task", "shell", "pty_shell", "git", "agent_send", "agent_reply", "agent_cancel", "agent_contract"} {
		if allowed, _ := gate.Allow(name, json.RawMessage(`{}`)); allowed {
			t.Fatalf("opaque/mutating tool admitted: %s", name)
		}
	}
	for _, raw := range []string{`{`, `null`, `{}`, `{"action":"edit"}`, `{"action":"READ"}`, `{"action":"unknown"}`} {
		if allowed, _ := gate.Allow("fs", json.RawMessage(raw)); allowed {
			t.Fatalf("unclassified fs action admitted: %s", raw)
		}
	}
	if allowed, _ := gate.Allow("agent_contract", json.RawMessage(`{"action":"readiness"}`)); !allowed {
		t.Fatal("contract observation blocked")
	}
}
