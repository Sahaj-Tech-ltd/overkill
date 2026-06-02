// Package subagent — contract enforcement at the child's tool boundary.
//
// The enforcer wraps any tool the child uses so that filesystem-mutating
// calls are checked against contract Scope/OutOfScope before execution.
// Out-of-scope writes are denied with a ContractViolation and never reach
// the underlying tool.
package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Tool is the minimal tool surface the enforcer wraps. Defined locally to
// avoid an import cycle with internal/tools (which already depends on this
// package via the delegate tool).
type Tool interface {
	Name() string
	Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

// AuditEntry is one row of the per-child contract-audit log.
type AuditEntry struct {
	Timestamp time.Time `json:"ts"`
	Tool      string    `json:"tool"`
	Path      string    `json:"path,omitempty"`
	Allowed   bool      `json:"allowed"`
	Reason    string    `json:"reason,omitempty"`
	ArgsLen   int       `json:"args_len"`
}

// AuditWriter persists AuditEntry rows. Returns the path it wrote to (if any).
type AuditWriter interface {
	Write(entry AuditEntry) error
	Path() string
}

// fileAuditWriter appends JSON lines under ~/.overkill/subagents/<id>/contract-audit.jsonl.
type fileAuditWriter struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// NewFileAuditWriter prepares a writer rooted at baseDir/<id>. baseDir is
// usually ~/.overkill/subagents. Returns nil writer + nil error when the dir
// can't be created (graceful degradation; auditing is best-effort).
func NewFileAuditWriter(baseDir, id string) (*fileAuditWriter, error) {
	dir := filepath.Join(baseDir, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("audit: mkdir: %w", err)
	}
	p := filepath.Join(dir, "contract-audit.jsonl")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("audit: open: %w", err)
	}
	return &fileAuditWriter{path: p, f: f}, nil
}

func (w *fileAuditWriter) Write(e AuditEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, _ := json.Marshal(e)
	if _, err := w.f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func (w *fileAuditWriter) Path() string { return w.path }

func (w *fileAuditWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}

// memAuditWriter is an in-memory audit sink for tests.
type memAuditWriter struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// NewMemAuditWriter returns an in-memory audit writer. Useful in tests.
func NewMemAuditWriter() *memAuditWriter { return &memAuditWriter{} }

func (w *memAuditWriter) Write(e AuditEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, e)
	return nil
}

func (w *memAuditWriter) Path() string { return "(memory)" }

// Entries returns a copy of all recorded entries.
func (w *memAuditWriter) Entries() []AuditEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]AuditEntry, len(w.entries))
	copy(out, w.entries)
	return out
}

// writeLikeTools is the set of tool names known to mutate the filesystem.
// Anything else is allowed through unchecked. The list is intentionally
// conservative: the enforcer prefers false negatives (over-allow) to false
// positives that would cripple a legitimate child.
var writeLikeTools = map[string]bool{
	"fs_write":        true,
	"fs":              false, // depends on action; handled below
	"patch":           true,
	"worktree_add":    true,
	"worktree_remove": true,
}

// shellWriteRe matches the leading verb of a shell command that suggests a
// write or destructive operation. Mirrors the heuristic used by the security scanner.
// D-3: Added ln, dd, truncate, rename, unlink, rsync to close scope enforcement gaps.
var shellWriteRe = regexp.MustCompile(`^\s*(rm|mv|cp|mkdir|touch|tee|chmod|chown|sed\s+-i|>{1,2}\s*\S+|cat\s*>{1,2}|ln(\s+-s)?|dd(\s+if=)?|truncate|rename|unlink|rsync(\s+.*--delete)?)`)

// ToolWithPath is a small interface that, if implemented by a Tool, lets the
// enforcer skip the JSON-decode dance and ask the tool directly which path
// it intends to mutate. Optional — most tools won't implement it.
type ToolWithPath interface {
	IntendedPath(input json.RawMessage) string
}

// EnforcedTool wraps a Tool and gates its Execute call through a Contract.
type EnforcedTool struct {
	inner    Tool
	contract *Contract
	audit    AuditWriter
	violCh   chan<- ContractViolation
}

// NewEnforcedTool wraps inner with contract enforcement. violCh, if non-nil,
// receives every denied call (best-effort, non-blocking).
func NewEnforcedTool(inner Tool, contract *Contract, audit AuditWriter, violCh chan<- ContractViolation) *EnforcedTool {
	return &EnforcedTool{
		inner:    inner,
		contract: contract,
		audit:    audit,
		violCh:   violCh,
	}
}

func (e *EnforcedTool) Name() string { return e.inner.Name() }

func (e *EnforcedTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	name := e.inner.Name()
	path := e.extractPath(input)

	// Decide whether this call is write-like.
	writeLike := isWriteLikeCall(name, input)

	if !writeLike {
		// Not a write — pass through. Audit is intentionally noisy: we want
		// every relevant call recorded, not just denials.
		_ = e.writeAudit(name, path, true, "", input)
		return e.inner.Execute(ctx, input)
	}

	// Write-like: gate by scope.
	if path == "" {
		// Cannot determine a path → fail safe by denying when scope is
		// non-empty (the child is trying to write but won't say where).
		if len(e.contract.Scope) > 0 || len(e.contract.OutOfScope) > 0 {
			reason := "write-like tool call without a determinable path"
			_ = e.writeAudit(name, "", false, reason, input)
			v := ContractViolation{
				Criterion: "scope",
				Reason:    reason,
				Evidence:  fmt.Sprintf("tool=%s", name),
			}
			e.notify(v)
			return nil, v
		}
	} else {
		ok, why := e.contract.CheckScope(path)
		if !ok {
			_ = e.writeAudit(name, path, false, why, input)
			v := ContractViolation{
				Criterion: "scope",
				Reason:    why,
				Evidence:  fmt.Sprintf("tool=%s path=%s", name, path),
			}
			e.notify(v)
			return nil, v
		}
	}

	_ = e.writeAudit(name, path, true, "", input)
	return e.inner.Execute(ctx, input)
}

func (e *EnforcedTool) notify(v ContractViolation) {
	if e.violCh == nil {
		return
	}
	select {
	case e.violCh <- v:
	default:
	}
}

func (e *EnforcedTool) writeAudit(tool, path string, allowed bool, reason string, input json.RawMessage) error {
	if e.audit == nil {
		return nil
	}
	return e.audit.Write(AuditEntry{
		Timestamp: time.Now().UTC(),
		Tool:      tool,
		Path:      path,
		Allowed:   allowed,
		Reason:    reason,
		ArgsLen:   len(input),
	})
}

// extractPath pulls the most likely target path out of a tool's JSON input.
// Looks at common field names (path, file, target, dst). Best-effort.
func (e *EnforcedTool) extractPath(input json.RawMessage) string {
	if t, ok := e.inner.(ToolWithPath); ok {
		if p := t.IntendedPath(input); p != "" {
			return p
		}
	}
	var generic map[string]any
	if err := json.Unmarshal(input, &generic); err != nil {
		return ""
	}
	for _, k := range []string{"path", "file", "target", "dst", "destination", "filename"} {
		if v, ok := generic[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// isWriteLikeCall reports whether this tool invocation is filesystem-mutating.
// Shell commands are inspected for leading destructive verbs.
func isWriteLikeCall(name string, input json.RawMessage) bool {
	if writeLikeTools[name] {
		return true
	}
	switch name {
	case "shell", "pty_shell":
		var sh struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(input, &sh); err != nil {
			return false
		}
		return shellWriteRe.MatchString(sh.Command)
	case "fs":
		var f struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(input, &f); err != nil {
			return false
		}
		switch strings.ToLower(f.Action) {
		case "write", "create", "delete", "remove", "mkdir":
			return true
		}
	}
	return false
}
