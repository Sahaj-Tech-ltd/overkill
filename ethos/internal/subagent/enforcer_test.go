package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// fakeTool implements the local Tool interface for tests.
type fakeTool struct {
	name     string
	called   bool
	lastArgs json.RawMessage
	out      json.RawMessage
	err      error
}

func (f *fakeTool) Name() string { return f.name }
func (f *fakeTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	f.called = true
	f.lastArgs = in
	return f.out, f.err
}

func TestEnforcer_NonWriteToolPassesThrough(t *testing.T) {
	inner := &fakeTool{name: "fs_read", out: json.RawMessage(`"ok"`)}
	c := &Contract{Scope: []string{"internal/foo"}}
	audit := NewMemAuditWriter()
	e := NewEnforcedTool(inner, c, audit, nil)

	out, err := e.Execute(context.Background(), json.RawMessage(`{"path":"anywhere"}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(out) != `"ok"` {
		t.Fatalf("unexpected out: %s", out)
	}
	if !inner.called {
		t.Fatal("expected inner tool to be called")
	}
	if len(audit.Entries()) != 1 || !audit.Entries()[0].Allowed {
		t.Fatalf("audit entry mismatch: %+v", audit.Entries())
	}
}

func TestEnforcer_WriteInScopeAllowed(t *testing.T) {
	inner := &fakeTool{name: "fs_write", out: json.RawMessage(`"ok"`)}
	c := &Contract{Scope: []string{"internal/foo"}}
	e := NewEnforcedTool(inner, c, nil, nil)

	out, err := e.Execute(context.Background(), json.RawMessage(`{"path":"internal/foo/bar.go"}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(out) != `"ok"` {
		t.Fatalf("unexpected out: %s", out)
	}
}

func TestEnforcer_WriteOutOfScopeDenied(t *testing.T) {
	inner := &fakeTool{name: "fs_write"}
	c := &Contract{Scope: []string{"internal/foo"}}
	violCh := make(chan ContractViolation, 1)
	audit := NewMemAuditWriter()
	e := NewEnforcedTool(inner, c, audit, violCh)

	_, err := e.Execute(context.Background(), json.RawMessage(`{"path":"internal/other/x.go"}`))
	var v ContractViolation
	if !errors.As(err, &v) {
		t.Fatalf("expected ContractViolation, got %T %v", err, err)
	}
	if v.Criterion != "scope" {
		t.Fatalf("violation criterion = %q want scope", v.Criterion)
	}
	if inner.called {
		t.Fatal("inner tool should not have been called on denial")
	}
	select {
	case got := <-violCh:
		if got.Criterion != "scope" {
			t.Fatalf("channel violation criterion = %q", got.Criterion)
		}
	default:
		t.Fatal("expected violation on channel")
	}
	entries := audit.Entries()
	if len(entries) != 1 || entries[0].Allowed {
		t.Fatalf("expected one denied audit entry, got %+v", entries)
	}
}

func TestEnforcer_WriteWithoutPathDeniedWhenScoped(t *testing.T) {
	inner := &fakeTool{name: "fs_write"}
	c := &Contract{Scope: []string{"internal/foo"}}
	e := NewEnforcedTool(inner, c, nil, nil)

	_, err := e.Execute(context.Background(), json.RawMessage(`{"contents":"x"}`))
	if err == nil {
		t.Fatal("expected denial when no path on a scoped contract")
	}
}

func TestEnforcer_ShellWriteCommandGated(t *testing.T) {
	inner := &fakeTool{name: "shell"}
	c := &Contract{OutOfScope: []string{"/etc/passwd"}}
	e := NewEnforcedTool(inner, c, nil, nil)

	// Non-write shell — pass through.
	if _, err := e.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`)); err != nil {
		t.Fatalf("echo should pass: %v", err)
	}
	if !inner.called {
		t.Fatal("echo should have invoked inner")
	}

	// Write shell with no determinable path; scope is set → fail safe.
	inner.called = false
	if _, err := e.Execute(context.Background(), json.RawMessage(`{"command":"rm /tmp/x"}`)); err == nil {
		t.Fatal("rm with scoped contract and no path should be denied")
	}

	// Truly unscoped contract — write shell passes through.
	inner2 := &fakeTool{name: "shell"}
	e2 := NewEnforcedTool(inner2, &Contract{}, nil, nil)
	if _, err := e2.Execute(context.Background(), json.RawMessage(`{"command":"rm /tmp/x"}`)); err != nil {
		t.Fatalf("rm with empty contract should pass: %v", err)
	}
	if !inner2.called {
		t.Fatal("rm should have invoked inner under empty contract")
	}
}

func TestEnforcer_FsActionBranch(t *testing.T) {
	inner := &fakeTool{name: "fs"}
	c := &Contract{Scope: []string{"allowed"}}
	e := NewEnforcedTool(inner, c, nil, nil)

	// "read" action — pass through even with out-of-scope path.
	inner.called = false
	if _, err := e.Execute(context.Background(), json.RawMessage(`{"action":"read","path":"forbidden/file"}`)); err != nil {
		t.Fatalf("read should pass: %v", err)
	}
	if !inner.called {
		t.Fatal("read should have invoked inner")
	}

	// "write" action with out-of-scope path — denied.
	inner.called = false
	_, err := e.Execute(context.Background(), json.RawMessage(`{"action":"write","path":"forbidden/file"}`))
	if err == nil || inner.called {
		t.Fatal("write to forbidden path should be denied")
	}
}

func TestFileAuditWriter(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileAuditWriter(dir, "child-1")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer w.Close()
	if err := w.Write(AuditEntry{Tool: "fs_write", Allowed: true}); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := filepath.Join(dir, "child-1", "contract-audit.jsonl")
	if w.Path() != want {
		t.Fatalf("path = %q want %q", w.Path(), want)
	}
}
