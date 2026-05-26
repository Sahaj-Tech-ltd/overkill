// Package main — `overkill estop` (master plan §7.1) is the emergency stop.
//
// Two-phase escalation:
//
//  1. Graceful RPC: dial the daemon socket, send the `estop` op. The
//     daemon cancels every pending alarm + any future in-flight work
//     contexts and replies with a count. Tool receipt chains stay
//     intact; the daemon keeps running so subsequent commands work.
//
//  2. Signal cascade (fallback): if the RPC fails OR the user passes
//     --force, fall back to SIGTERM the daemon + session owners,
//     SIGKILL survivors after 3s. This is the "axe" — use when the
//     daemon itself is the thing being stopped.
//
// Designed for "the agent is doing the wrong thing — stop it now".
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

var estopForce bool

var estopCmd = &cobra.Command{
	Use:   "estop",
	Short: "Emergency-stop every running overkill process",
	Long: `Sends SIGTERM to the daemon and any session owners listed under
~/.overkill/sessions/*/owner.pid. Escalates to SIGKILL after 3 seconds for
processes that don't exit. Returns success even if nothing was running.`,
	RunE: runEstop,
}

func init() {
	estopCmd.Flags().BoolVar(&estopForce, "force", false, "skip graceful RPC; signal-cascade immediately")
	rootCmd.AddCommand(estopCmd)
}

func runEstop(cmd *cobra.Command, args []string) error {
	// Phase 0: try graceful RPC first unless --force.
	if !estopForce {
		if handled := tryGracefulEStop(); handled {
			return nil
		}
	}

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

// tryGracefulEStop dials the daemon socket and asks for a clean halt.
// Returns true when the daemon responded — caller skips the signal
// cascade. Returns false when the daemon isn't reachable or the call
// errored; caller proceeds with signals.
//
// The graceful path keeps the daemon alive (so subsequent commands
// work) but interrupts every pending alarm + future in-flight task.
// Tool receipt chains are preserved.
func tryGracefulEStop() bool {
	path, err := daemon.SocketPath()
	if err != nil {
		return false
	}
	client := daemon.NewClient(path).WithTimeout(3 * time.Second)
	raw, err := client.Call("estop", nil)
	if err != nil {
		// ErrDaemonDown is expected when the daemon isn't running —
		// silently fall through to signal cascade (which will also
		// find nothing to stop, but at least surfaces the right msg).
		return false
	}
	var resp struct {
		Halted int `json:"halted"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		// Daemon responded but with garbage — be conservative and
		// fall back rather than reporting fake success.
		return false
	}
	if resp.Halted == 0 {
		fmt.Printf("%s✓ daemon reached — nothing was running%s\n", colorGreen, colorReset)
	} else {
		fmt.Printf("%s✓ halted %d in-flight task(s) via daemon RPC%s\n", colorGreen, resp.Halted, colorReset)
	}
	return true
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
