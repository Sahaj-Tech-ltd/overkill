package subagent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Test 1: Delegate to an unregistered agent → error contains "not found"
// ---------------------------------------------------------------------------

func TestExternalDelegator_AgentNotFound(t *testing.T) {
	d := NewExternalDelegator("", 5*time.Second, nil)

	_, err := d.Delegate(context.Background(), "nonexistent", "do something")
	if err == nil {
		t.Fatal("expected error for missing agent, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, should contain 'not found'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Test 2: Register agent with a non-existent command → error about PATH
// ---------------------------------------------------------------------------

func TestExternalDelegator_CommandNotInPath(t *testing.T) {
	d := NewExternalDelegator("", 5*time.Second, nil)
	d.Register(AgentDef{
		Name:    "ghost",
		Command: "overkill-definitely-not-real-xyz",
	})

	_, err := d.Delegate(context.Background(), "ghost", "do something")
	if err == nil {
		t.Fatal("expected error for missing command, got nil")
	}
	if !strings.Contains(err.Error(), "not found in PATH") {
		t.Errorf("error = %q, should contain 'not found in PATH'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// Test 3: Register echo agent → result Summary contains the echoed text
// ---------------------------------------------------------------------------

func TestExternalDelegator_EchoCommand(t *testing.T) {
	d := NewExternalDelegator("", 5*time.Second, nil)
	d.Register(AgentDef{
		Name:    "echoer",
		Command: "echo",
		Args:    []string{"hello from subagent"},
	})

	res, err := d.Delegate(context.Background(), "echoer", "say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want %q", res.Status, "completed")
	}
	if !strings.Contains(res.Summary, "hello from subagent") {
		t.Errorf("Summary = %q, should contain 'hello from subagent'", res.Summary)
	}
}

// ---------------------------------------------------------------------------
// Test 4: Register sleep agent with short timeout → status "timeout"
//
// Uses "sh -c 'exec sleep 10'" so the shell replaces itself with sleep,
// ensuring exec.CommandContext can kill the actual sleeping process.
// Context JSON is now delivered via stdin (C1 fix) so it won't
// interfere with the shell command.
// ---------------------------------------------------------------------------

func TestExternalDelegator_Timeout(t *testing.T) {
	d := NewExternalDelegator("", 50*time.Millisecond, nil)
	d.Register(AgentDef{
		Name:    "sleeper",
		Command: "sh",
		Args:    []string{"-c", "exec sleep 10"},
	})

	res, err := d.Delegate(context.Background(), "sleeper", "sleep a lot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != "timeout" {
		t.Errorf("Status = %q, want %q", res.Status, "timeout")
	}
	if res.ExitReason != "timeout" {
		t.Errorf("ExitReason = %q, want %q", res.ExitReason, "timeout")
	}
}

// ---------------------------------------------------------------------------
// Test 5: Register 2 agents → ListAgents returns 2
// ---------------------------------------------------------------------------

func TestExternalDelegator_ListAgents(t *testing.T) {
	d := NewExternalDelegator("", 5*time.Second, nil)
	d.Register(AgentDef{
		Name:    "agent-alpha",
		Command: "echo",
	})
	d.Register(AgentDef{
		Name:    "agent-beta",
		Command: "echo",
	})

	agents := d.ListAgents()
	if len(agents) != 2 {
		t.Fatalf("ListAgents() returned %d agents, want 2", len(agents))
	}

	// Verify both names are present (order is indeterminate).
	names := map[string]bool{}
	for _, a := range agents {
		names[a.Name] = true
	}
	if !names["agent-alpha"] {
		t.Error("ListAgents() missing 'agent-alpha'")
	}
	if !names["agent-beta"] {
		t.Error("ListAgents() missing 'agent-beta'")
	}
}

// ---------------------------------------------------------------------------
// Test 6: DelegateWithExport with echo agent → status "completed"
// ---------------------------------------------------------------------------

func TestExternalDelegator_ContextExport(t *testing.T) {
	d := NewExternalDelegator("", 5*time.Second, nil)
	d.Register(AgentDef{
		Name:    "echoer",
		Command: "echo",
		Args:    []string{"done"},
	})

	export := ContextExport{
		SessionID: "sess-42",
		Goal:      "verify context export",
		Context: ExportContext{
			Language: "go",
		},
		OverkillVersion: "0.1.0",
	}

	res, err := d.DelegateWithExport(context.Background(), "echoer", export)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want %q", res.Status, "completed")
	}
	if res.ExitReason != "completed" {
		t.Errorf("ExitReason = %q, want %q", res.ExitReason, "completed")
	}
	if !strings.Contains(res.Summary, "done") {
		t.Errorf("Summary = %q, should contain 'done'", res.Summary)
	}
}
