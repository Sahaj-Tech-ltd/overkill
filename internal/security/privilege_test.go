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
		{"shell-echo", ``, true},
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
		{"unknown", false},
	}
	for _, tc := range cases {
		raw, _ := json.Marshal(map[string]string{"action": tc.action})
		if got := IsWriteLikeTool("fs", raw); got != tc.want {
			t.Errorf("fs/%s got=%v want=%v", tc.action, got, tc.want)
		}
	}
}
