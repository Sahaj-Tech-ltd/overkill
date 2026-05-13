// Package main — `overkill estop` (master plan §7.1) is the emergency stop.
//
// Sends SIGTERM to:
//   - the running daemon (via ~/.overkill/daemon.pid)
//   - any other overkill processes the user is running, identified by reading
//     ~/.overkill/sessions/*/owner.pid sentinel files (best-effort)
//
// Designed for "the agent is doing the wrong thing — kill everything now".
// Falls back to SIGKILL after a 3s grace period for non-responsive PIDs.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

var estopCmd = &cobra.Command{
	Use:   "estop",
	Short: "Emergency-stop every running overkill process",
	Long: `Sends SIGTERM to the daemon and any session owners listed under
~/.overkill/sessions/*/owner.pid. Escalates to SIGKILL after 3 seconds for
processes that don't exit. Returns success even if nothing was running.`,
	RunE: runEstop,
}

func init() {
	rootCmd.AddCommand(estopCmd)
}

func runEstop(cmd *cobra.Command, args []string) error {
	pids := collectEstopTargets()
	if len(pids) == 0 {
		fmt.Printf("%snothing to stop — no daemon, no session owners%s\n", colorYellow, colorReset)
		return nil
	}

	// Phase 1: SIGTERM all.
	for _, pid := range pids {
		if err := syscall.Kill(pid, syscall.SIGTERM); err == nil {
			fmt.Printf("%s→ SIGTERM %d%s\n", colorYellow, pid, colorReset)
		}
	}

	// Phase 2: wait up to 3s, escalate to SIGKILL on survivors.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range pids {
			if pidIsRunning(pid) {
				alive = true
				break
			}
		}
		if !alive {
			fmt.Printf("%s✓ all processes exited cleanly%s\n", colorGreen, colorReset)
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}

	for _, pid := range pids {
		if pidIsRunning(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			fmt.Printf("%s→ SIGKILL %d (did not exit on SIGTERM)%s\n", colorYellow, pid, colorReset)
		}
	}
	return nil
}

// collectEstopTargets returns every PID worth stopping, deduplicated.
func collectEstopTargets() []int {
	seen := map[int]bool{}
	add := func(pid int) {
		if pid > 0 && pid != os.Getpid() && !seen[pid] && pidIsRunning(pid) {
			seen[pid] = true
		}
	}

	// 1. The daemon.
	if pid, err := readPID(); err == nil {
		add(pid)
	}

	// 2. Session owners.
	if home, err := os.UserHomeDir(); err == nil {
		root := filepath.Join(home, ".overkill", "sessions")
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			ownerFile := filepath.Join(root, e.Name(), "owner.pid")
			if data, err := os.ReadFile(ownerFile); err == nil {
				if pid, err := strconv.Atoi(string(data)); err == nil {
					add(pid)
				}
			}
		}
	}

	out := make([]int, 0, len(seen))
	for pid := range seen {
		out = append(out, pid)
	}
	return out
}
