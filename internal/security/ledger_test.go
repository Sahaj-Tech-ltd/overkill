package security

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLedgerAppendAndFilter(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLedger(filepath.Join(dir, "perm.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(LedgerEntry{Tool: "shell", Args: "ls", Decision: "allow_once", Risk: "low"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(LedgerEntry{Tool: "fs", Args: "rm -rf /", Decision: "deny", Risk: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(LedgerEntry{Tool: "shell", Args: "echo hi", Decision: "allow_session"}); err != nil {
		t.Fatal(err)
	}
	all := l.Entries()
	if len(all) != 3 {
		t.Fatalf("want 3, got %d", len(all))
	}
	allowed := l.Filter(func(e LedgerEntry) bool {
		return strings.HasPrefix(e.Decision, "allow")
	})
	if len(allowed) != 2 {
		t.Errorf("want 2 allows, got %d", len(allowed))
	}
}

func TestLedgerPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.jsonl")
	l1, _ := NewLedger(path)
	l1.Append(LedgerEntry{Tool: "x", Decision: "allow_once"})

	l2, _ := NewLedger(path)
	if got := l2.Entries(); len(got) != 1 || got[0].Tool != "x" {
		t.Errorf("reload failed: %+v", got)
	}
}
