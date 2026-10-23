// Package hooks — user-defined hooks loaded from disk (master plan §6.3).
//
// Layout: ~/.overkill/hooks/<point>/<name>.sh on Unix, or .ps1/.exe on Windows.
//
//	point ∈ {before_tool_call, after_tool_call, on_session_start,
//	         on_session_end, on_error, before_compaction, after_compaction,
//	         after_turn}
//
// Each script is invoked with the JSON-serialized Event piped to stdin.
// Scripts have 5 seconds to finish; non-zero exit codes are logged but do
// NOT block the agent (hooks are observability, not gating). Make scripts
// executable (`chmod +x`) on Unix. Windows hooks must be owned by the current
// user, with modification rights limited to that user, Administrators and SYSTEM.
// PowerShell hooks run without profiles and retain the machine execution policy.
// Unsafe or unsupported files are skipped. Timeout cancellation kills the hook
// process group on Unix and its entire Job Object on Windows.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadFromDir walks dir/<point>/ and registers every safe executable hook
// as a Hook. Returns the count of hooks registered. Missing dir is fine
// (returns 0, nil).
func LoadFromDir(reg *Registry, dir string) (int, error) {
	if reg == nil {
		return 0, fmt.Errorf("hooks: nil registry")
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("hooks: %s is not a directory", dir)
	}
	if !isTrustedDirectory(dir) {
		return 0, fmt.Errorf("hooks: directory is not a trusted user-owned directory: %s", dir)
	}

	count := 0
	for _, point := range allPoints {
		pointDir := filepath.Join(dir, string(point))
		entries, err := os.ReadDir(pointDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return count, fmt.Errorf("hooks: read %s: %w", pointDir, err)
		}
		if !isTrustedDirectory(pointDir) {
			return count, fmt.Errorf("hooks: untrusted hook point directory: %s", pointDir)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			full := filepath.Join(pointDir, e.Name())
			if !isExecutable(full) {
				continue
			}
			scriptPath := full
			scriptName := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			hook := Hook{
				Name:  fmt.Sprintf("file:%s/%s", point, scriptName),
				Point: point,
				Fn:    makeShellHook(scriptPath),
			}
			if err := reg.Register(hook); err != nil {
				continue
			}
			count++
		}
	}
	return count, nil
}

// NewRegistryFromDir loads only the application owner's hooks directory, never
// scripts from a workspace supplied by an agent or remote peer. Each session
// receives its own registry; changes take effect for newly constructed sessions.
func NewRegistryFromDir(dir string) (*Registry, error) {
	reg := NewRegistry()
	if _, err := LoadFromDir(reg, dir); err != nil {
		return nil, err
	}
	return reg, nil
}

var allPoints = []HookPoint{
	BeforeToolCall, AfterToolCall, OnSessionStart, OnSessionEnd,
	OnError, BeforeCompaction, AfterCompaction, AfterTurn,
}

// makeShellHook returns a HookFunc that pipes the JSON event to the script.
// 5s timeout; stdout/stderr captured but only stderr is logged on non-zero
// exit (stdout is treated as quiet by design).
func makeShellHook(scriptPath string) HookFunc {
	return func(ctx context.Context, event Event) (context.Context, error) {
		if !isTrustedDirectory(filepath.Dir(filepath.Dir(scriptPath))) || !isTrustedDirectory(filepath.Dir(scriptPath)) || !isExecutable(scriptPath) {
			return ctx, fmt.Errorf("hooks: script is no longer trusted: %s", scriptPath)
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return ctx, nil // best-effort: don't fail the agent on a bad event
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd, err := newHookCommand(cctx, scriptPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[hook] %s could not start: %v\n", filepath.Base(scriptPath), err)
			return ctx, nil
		}
		cmd.Stdin = bytes.NewReader(raw)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := runHookCommand(cctx, cmd); err != nil {
			fmt.Fprintf(os.Stderr, "[hook] %s exited %v: %s\n",
				filepath.Base(scriptPath), err, strings.TrimSpace(stderr.String()))
		}
		return ctx, nil
	}
}
