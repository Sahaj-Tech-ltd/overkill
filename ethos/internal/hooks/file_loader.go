// Package hooks — user-defined hooks loaded from disk (master plan §6.3).
//
// Layout: ~/.ethos/hooks/<point>/<name>.sh
//   point ∈ {before_tool_call, after_tool_call, on_session_start,
//            on_session_end, on_error, before_compaction, after_compaction}
//
// Each script is invoked with the JSON-serialized Event piped to stdin.
// Scripts have 5 seconds to finish; non-zero exit codes are logged but do
// NOT block the agent (hooks are observability, not gating). Make scripts
// executable (`chmod +x`) — non-executable files are silently skipped.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LoadFromDir walks dir/<point>/*.sh and registers every executable script
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

	count := 0
	for _, point := range allPoints {
		pointDir := filepath.Join(dir, string(point))
		entries, err := os.ReadDir(pointDir)
		if err != nil {
			continue // missing point dir is fine
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

var allPoints = []HookPoint{
	BeforeToolCall, AfterToolCall, OnSessionStart, OnSessionEnd,
	OnError, BeforeCompaction, AfterCompaction,
}

// makeShellHook returns a HookFunc that pipes the JSON event to the script.
// 5s timeout; stdout/stderr captured but only stderr is logged on non-zero
// exit (stdout is treated as quiet by design).
func makeShellHook(scriptPath string) HookFunc {
	return func(ctx context.Context, event Event) (context.Context, error) {
		raw, err := json.Marshal(event)
		if err != nil {
			return ctx, nil // best-effort: don't fail the agent on a bad event
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cctx, scriptPath)
		cmd.Stdin = bytes.NewReader(raw)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "[hook] %s exited %v: %s\n",
				filepath.Base(scriptPath), err, strings.TrimSpace(stderr.String()))
		}
		return ctx, nil
	}
}

// isExecutable reports whether the file's user-execute bit is set. We don't
// require g+x or o+x — just owner.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().Perm()&0o100 != 0
}
