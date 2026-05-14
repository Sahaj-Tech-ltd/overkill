// Package main — `overkill daemon` boots background services: cron scheduler,
// automation alarm clock, routine engine. Foreground process by default;
// runs until SIGINT/SIGTERM. PID file at ~/.overkill/daemon.pid for `stop`/
// `status` to find a running instance.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/spf13/cobra"

	"encoding/json"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

// daemonStartedAt is captured on Start so the `ping` RPC can report
// uptime without re-statting the pidfile.
var daemonStartedAt time.Time

// daemonLedger holds the live background-task ledger for the running daemon.
// Goroutines reading or writing it are coordinated via the ledger's own mutex.
var daemonLedger = automation.NewLedger(500)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the background daemon (cron + automation)",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start background daemon (foreground process)",
	RunE:  runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Signal a running daemon to exit",
	RunE:  runDaemonStop,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report daemon PID and uptime",
	RunE:  runDaemonStatus,
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonInstallCmd)
	rootCmd.AddCommand(daemonCmd)
}

func daemonHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".overkill")
	return dir, os.MkdirAll(dir, 0o755)
}

func pidFilePath() (string, error) {
	dir, err := daemonHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.pid"), nil
}

func writePIDFile() error {
	p, err := pidFilePath()
	if err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())), 0o644)
}

func readPID() (int, error) {
	p, err := pidFilePath()
	if err != nil {
		return 0, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(b))
}

func runDaemonStart(cmd *cobra.Command, args []string) error {
	if pid, _ := readPID(); pid > 0 {
		if pidIsRunning(pid) {
			return fmt.Errorf("daemon already running (pid %d) — use 'overkill daemon stop' first", pid)
		}
	}

	if err := writePIDFile(); err != nil {
		return fmt.Errorf("daemon: pidfile: %w", err)
	}
	defer func() {
		if p, _ := pidFilePath(); p != "" {
			_ = os.Remove(p)
		}
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	home, err := daemonHomeDir()
	if err != nil {
		return err
	}

	// Open per-subsystem Badger DBs. Each lives in its own subdir so a
	// schema change in one doesn't corrupt the other. Best-effort: a failure
	// in one subsystem doesn't prevent the others from starting.
	cronDir := filepath.Join(home, "cron")
	autoDir := filepath.Join(home, "automation")
	_ = os.MkdirAll(cronDir, 0o755)
	_ = os.MkdirAll(autoDir, 0o755)

	var (
		wg         sync.WaitGroup
		startedCron bool
		startedAuto bool
	)

	if cronDB, err := badger.Open(badger.DefaultOptions(cronDir).WithLoggingLevel(badger.ERROR)); err == nil {
		defer cronDB.Close()
		store := cron.NewBadgerJobStore(cronDB)
		sched, err := cron.NewScheduler(cron.Config{
			Store:  store,
			OnFire: shellOnFire,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "daemon: cron init failed: %v\n", err)
		} else {
			sched.Start()
			defer sched.Stop()
			fmt.Printf("%s✓ cron scheduler started (%d jobs)%s\n", colorGreen, len(sched.ListJobs()), colorReset)
			startedCron = true
		}
	} else {
		fmt.Fprintf(os.Stderr, "daemon: cron Badger open failed: %v\n", err)
	}

	var daemonAlarm *automation.AlarmClock
	if autoDB, err := badger.Open(badger.DefaultOptions(autoDir).WithLoggingLevel(badger.ERROR)); err == nil {
		defer autoDB.Close()
		sopStore := automation.NewBadgerSOPStore(autoDB)
		_ = automation.NewSOPEngine(sopStore, shellExecutor)

		alarmStore := automation.NewBadgerAlarmStore(autoDB)
		daemonAlarm = automation.NewAlarmClockWithStore(
			alarmDispatchFire(daemonLedger),
			alarmStore,
		)
		daemonAlarm.Start()
		defer daemonAlarm.Stop()
		fmt.Printf("%s✓ automation engine started (%d alarms)%s\n", colorGreen, len(daemonAlarm.List()), colorReset)

		// Routine engine (§7.1 Layer 4). Persists across restarts
		// so registered event→action rules survive reboots with
		// their cooldown timers intact.
		routineStore := automation.NewBadgerRoutineStore(autoDB)
		routineEngine, rerr := automation.NewRoutineEngineWithStore(shellExecutor, routineStore)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "daemon: routine load: %v\n", rerr)
		}
		daemonRoutines = routineEngine
		fmt.Printf("%s✓ routine engine started (%d routines)%s\n", colorGreen, len(routineEngine.List()), colorReset)

		// Flow store for Task Flow durable resume (§7.1 Layer 7).
		// Shares the same Badger DB as alarms/SOPs.
		daemonFlowStore = agent.NewBadgerFlowStore(autoDB)
		startedAuto = true
	} else {
		fmt.Fprintf(os.Stderr, "daemon: automation Badger open failed: %v\n", err)
	}

	if !startedCron && !startedAuto {
		return errors.New("daemon: nothing started — both cron and automation failed")
	}

	// Daily snapshot tick (master plan §4.20). Fire on start, then every 24h.
	wg.Add(1)
	go func() {
		defer wg.Done()
		dailySnapshotTick(ctx)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dailySnapshotTick(ctx)
			}
		}
	}()
	fmt.Printf("%s✓ daily snapshot ticker armed%s\n", colorGreen, colorReset)

	// Background task ledger sweeper (§7.1 Layer 6). Marks stuck
	// running/queued tasks as `lost` after a 5-min grace when the
	// owning PID is dead or never recorded. 60s scan cadence by
	// default — slow enough to not waste CPU, fast enough that the
	// user sees stale rows update in roughly one breath.
	sweeper := automation.NewSweeper(daemonLedger, automation.SweeperConfig{
		OnLost: func(t automation.LedgerTask) {
			fmt.Fprintf(os.Stderr, "ledger: lost task %s (%s/%s): %s\n",
				t.ID, t.Source, t.Name, t.Error)
		},
	})
	sweeper.Start(ctx)
	defer sweeper.Stop()
	fmt.Printf("%s✓ ledger sweeper started%s\n", colorGreen, colorReset)

	// Behavior monitor + failhypo extraction ticker (paper #48).
	// Runs Wall 4 detectors and the failed-hypothesis regex pass over
	// today's journal entries every 5 minutes. Findings land in the
	// alert store so the next TUI boot surfaces them.
	behaviorTickerStart(ctx, &wg)

	// RPC socket so the TUI / CLI / future webhook receivers can talk
	// to the running daemon without sharing in-process state. Bind
	// errors are non-fatal — the daemon still runs in standalone mode
	// (cron + automation fire on their own clocks), the user just can't
	// remote-control it. We log loud so misconfigured permissions are
	// obvious.
	daemonStartedAt = time.Now()
	sockPath, sockErr := daemon.SocketPath()
	var sock *daemon.Server
	if sockErr != nil {
		fmt.Fprintf(os.Stderr, "daemon: socket path resolve failed: %v\n", sockErr)
	} else {
		sock = daemon.NewServer(sockPath)
		sock.Register("ping", pingHandler)
		if daemonAlarm != nil {
			registerAlarmHandlers(sock, daemonAlarm)
			registerFlowHandlers(sock, daemonFlowStore, daemonAlarm)
		}
		if daemonRoutines != nil {
			registerRoutineHandlers(sock, daemonRoutines)
		}
		// Estop: graceful halt path. Cancels every pending alarm so
		// scheduled work doesn't fire after the user said "stop". If
		// the daemon's signal-trap also fires (from `overkill estop`'s
		// fallback), shutdown proceeds normally.
		sock.Register("estop", estopHandler(&daemonEStopBroadcaster{
			alarmCancelAll: func() int {
				if daemonAlarm == nil {
					return 0
				}
				n := 0
				for _, a := range daemonAlarm.List() {
					if a.Fired || a.Cancelled {
						continue
					}
					if daemonAlarm.Cancel(a.ID) {
						n++
					}
				}
				return n
			},
		}))
		if err := sock.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "daemon: socket bind failed: %v\n", err)
			sock = nil
		} else {
			fmt.Printf("%s✓ RPC socket at %s%s\n", colorGreen, sockPath, colorReset)
			defer sock.Stop()
		}
	}

	fmt.Printf("%soverkill daemon running (pid %d) — Ctrl-C to stop%s\n", colorBlue, os.Getpid(), colorReset)
	<-ctx.Done()
	fmt.Println()
	fmt.Printf("%soverkill daemon shutting down...%s\n", colorYellow, colorReset)
	wg.Wait()
	return nil
}

// shellOnFire is the default OnFire hook — runs the job's Command via /bin/sh.
// Output is logged and recorded into the daemon ledger so `overkill task list`
// can show what the daemon has done while the user was AFK.
func shellOnFire(j *cron.Job) error {
	if j.Command == "" {
		return errors.New("cron: job has no command")
	}
	t := daemonLedger.Begin("cron", j.Name)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", j.Command).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cron %s] FAILED: %v\n%s\n", j.Name, err, string(out))
		daemonLedger.Fail(t.ID, err)
		return err
	}
	fmt.Printf("[cron %s] ok\n", j.Name)
	daemonLedger.Complete(t.ID, "ok")
	return nil
}

// shellExecutor satisfies the SOPEngine action runner. Same shell-out shape.
func shellExecutor(action string) (string, error) {
	if action == "" {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", action).CombinedOutput()
	return string(out), err
}

func runDaemonStop(cmd *cobra.Command, args []string) error {
	pid, err := readPID()
	if err != nil {
		return fmt.Errorf("daemon: not running (no pidfile): %w", err)
	}
	if !pidIsRunning(pid) {
		fmt.Printf("%sStale pidfile (pid %d not running) — removing.%s\n", colorYellow, pid, colorReset)
		p, _ := pidFilePath()
		_ = os.Remove(p)
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("daemon: kill: %w", err)
	}
	fmt.Printf("%sSent SIGTERM to daemon (pid %d)%s\n", colorGreen, pid, colorReset)
	return nil
}

func runDaemonStatus(cmd *cobra.Command, args []string) error {
	pid, err := readPID()
	if err != nil {
		fmt.Printf("%sdaemon not running%s\n", colorYellow, colorReset)
		return nil
	}
	if !pidIsRunning(pid) {
		fmt.Printf("%sstale pidfile (pid %d not running)%s\n", colorYellow, pid, colorReset)
		return nil
	}
	p, _ := pidFilePath()
	info, _ := os.Stat(p)
	pidUptime := time.Duration(0)
	if info != nil {
		pidUptime = time.Since(info.ModTime())
	}

	// Probe the RPC socket. A running process with no socket means the
	// daemon booted but the listener bind failed (permissions?), which
	// is worth surfacing distinctly from "fully healthy".
	sockPath, _ := daemon.SocketPath()
	if sockPath == "" {
		fmt.Printf("%sdaemon running — pid=%d uptime=%s (no socket path)%s\n",
			colorYellow, pid, pidUptime.Round(time.Second), colorReset)
		return nil
	}
	client := daemon.NewClient(sockPath).WithTimeout(2 * time.Second)
	raw, err := client.Call("ping", nil)
	if err != nil {
		fmt.Printf("%sdaemon process up (pid=%d uptime=%s) but RPC unreachable: %v%s\n",
			colorYellow, pid, pidUptime.Round(time.Second), err, colorReset)
		return nil
	}
	var ping pingResponse
	if err := json.Unmarshal(raw, &ping); err != nil {
		fmt.Printf("%sdaemon running — pid=%d uptime=%s (ping response unparseable)%s\n",
			colorYellow, pid, pidUptime.Round(time.Second), colorReset)
		return nil
	}
	fmt.Printf("%sdaemon healthy — pid=%d uptime=%s version=%s%s\n",
		colorGreen, ping.PID, time.Duration(ping.UptimeSec)*time.Second, ping.Version, colorReset)
	return nil
}

// pingResponse is what the daemon returns to a `ping` RPC. Used by
// `overkill daemon status` to verify the daemon is not just running
// but actually responsive on its socket.
type pingResponse struct {
	PID       int    `json:"pid"`
	UptimeSec int64  `json:"uptime_sec"`
	Version   string `json:"version,omitempty"`
}

func pingHandler(ctx context.Context, req daemon.Request) (daemon.Response, error) {
	resp := pingResponse{
		PID:       os.Getpid(),
		UptimeSec: int64(time.Since(daemonStartedAt).Seconds()),
		Version:   appVersion(),
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return daemon.Response{}, err
	}
	return daemon.Response{Result: b}, nil
}

// appVersion returns the version baked in at build time (see ldflags
// in CI). Falls back to "dev" for local builds.
func appVersion() string {
	v := os.Getenv("OVERKILL_VERSION")
	if v == "" {
		return "dev"
	}
	return v
}

// systemdUnit is the print-and-copy systemd unit content. User saves
// it to ~/.config/systemd/user/overkill-daemon.service and runs:
//
//	systemctl --user daemon-reload
//	systemctl --user enable --now overkill-daemon
//
// The unit uses `Restart=on-failure` (not always) so a clean SIGTERM
// stop doesn't get auto-restarted, but a crash does.
const systemdUnit = `# Save to ~/.config/systemd/user/overkill-daemon.service
# Then: systemctl --user daemon-reload
#       systemctl --user enable --now overkill-daemon

[Unit]
Description=Overkill background daemon (cron + automation + RPC socket)
Documentation=https://github.com/Sahaj-Tech-ltd/overkill
After=network.target

[Service]
Type=simple
ExecStart=%s daemon start
Restart=on-failure
RestartSec=5s
# Logs go to journald; tail with: journalctl --user -u overkill-daemon -f
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
`

var daemonInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Print a systemd user unit you can save and enable",
	Long: "Generates a copy-paste-able systemd user unit pointing at the\n" +
		"current overkill binary. We deliberately don't write the file\n" +
		"ourselves — the user owns ~/.config/systemd, and unattended\n" +
		"writes there from a CLI tool are surprising.",
	RunE: runDaemonInstall,
}

func runDaemonInstall(cmd *cobra.Command, args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("daemon install: resolve own path: %w", err)
	}
	// os.Executable can return a symlink on some platforms; resolve so
	// the unit points at the real binary, not the launcher symlink.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	fmt.Printf(systemdUnit, exe)
	return nil
}

func pidIsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 probes liveness without actually delivering anything.
	return proc.Signal(syscall.Signal(0)) == nil
}
