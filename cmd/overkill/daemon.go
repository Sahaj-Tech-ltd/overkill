// Package main — `overkill daemon` boots background services: cron scheduler,
// automation alarm clock, routine engine. Foreground process by default;
// runs until SIGINT/SIGTERM. PID file at ~/.overkill/daemon.pid for `stop`/
// `status` to find a running instance.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor/health"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
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
	// Respect OVERKILL_HOME when set (e.g. container/profiles).
	if dir := os.Getenv("OVERKILL_HOME"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("daemon: creating OVERKILL_HOME dir %s: %w", dir, err)
		}
		return dir, nil
	}

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
	// Use O_CREATE|O_EXCL for atomic creation — prevents TOCTOU race
	// where two daemon start invocations both see no running PID and
	// both write a PID file, creating a split-brain daemon.
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("daemon already running (PID file exists at %s)", p)
		}
		return fmt.Errorf("daemon: pidfile: %w", err)
	}
	defer f.Close()
	_, err = f.Write([]byte(strconv.Itoa(os.Getpid())))
	return err
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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Open a single Postgres connection for all subsystems via internal/db.
	connString := os.Getenv("DATABASE_URL")
	if connString == "" && cfg != nil {
		connString = cfg.DatabaseURL
	}
	if connString == "" {
		return fmt.Errorf("daemon: DATABASE_URL must be set for Postgres backend")
	}
	database, err := db.Open(connString)
	if err != nil {
		return fmt.Errorf("daemon: open postgres: %w", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		return fmt.Errorf("daemon: migrate: %w", err)
	}

	// Health check sweep on boot (§7.1.5.2). Non-blocking — failures
	// are logged but never prevent daemon startup.
	if cfg != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result, err := health.RunDoctor(ctx, cfg, false) // core checks only on boot
			if err != nil {
				fmt.Fprintf(os.Stderr, "daemon: health sweep failed: %v\n", err)
				return
			}
			if len(result.Findings) > 0 {
				fmt.Fprintf(os.Stderr, "%s⚕ health sweep: %d findings (%d repaired, %d failed)%s\n",
					colorYellow, len(result.Findings), len(result.Repaired), len(result.Failed), colorReset)
			}
		}()
	}

	var (
		wg          sync.WaitGroup
		startedCron bool
		startedAuto bool
	)

	// ── Cron → Gateway bridge (§7.1 Layer 1) ──
	// Activity tracker records the last user activity timestamp so cron
	// output can be deferred until the user is idle. The output buffer
	// queues cron job output and auto-flushes via background goroutine
	// when cfg.Cron.IdleWindowSec (default 5 min) has passed since the last activity.
	activityTracker := cron.NewActivityTracker()

	// Apply configurable idle window (cron + activity).
	if cfg.Cron.IdleWindowSec > 0 {
		cron.SetIdleWindow(time.Duration(cfg.Cron.IdleWindowSec) * time.Second)
	}
	setCronIdleWindow(cfg.Cron.IdleWindowSec)

	// ── Gateway channels (§7.1 unified hub) ──
	// Start Telegram + Discord from config so cron output reaches users.
	// Falls back to shellOnFire when no gateways are configured.
	gwResult := startDaemonGateways(ctx, cfg, database, activityTracker.Record)

	outputBuffer := cron.NewOutputBuffer(
		cron.IdleWindow,
		activityTracker,
		func(job *cron.Job, output string) {
			fmt.Fprintf(os.Stderr, "[cron %s] buffered output (%d chars)\n", job.Name, len(output))
		},
	)
	defer outputBuffer.Stop()

	// Pick dispatcher: use daemon gateway hub if configured, else nil (shellOnFire).
	var cronDisp *gateway.Dispatcher
	if gwResult != nil {
		cronDisp = gwResult.Dispatcher
	}

	// Cron scheduler via Postgres.
	if cronStore, cerr := cron.NewPostgresJobStore(database); cerr == nil {
		sched, serr := cron.NewScheduler(cron.Config{
			Store:  cronStore,
			OnFire: gatewayOnFire(cronDisp, activityTracker, outputBuffer),
		})
		if serr != nil {
			fmt.Fprintf(os.Stderr, "daemon: cron init failed: %v\n", serr)
		} else {
			sched.Start()
			defer sched.Stop()
			fmt.Printf("%s✓ cron scheduler started (%d jobs)%s\n", colorGreen, len(sched.ListJobs()), colorReset)
			startedCron = true
		}
	} else {
		fmt.Fprintf(os.Stderr, "daemon: cron store open failed: %v\n", cerr)
	}

	var daemonAlarm *automation.AlarmClock
	// Automation engine via Postgres.
	sopStore, serr := automation.NewPostgresSOPStore(database)
	if serr != nil {
		fmt.Fprintf(os.Stderr, "daemon: sop store open failed: %v\n", serr)
	} else {
		daemonSOPEngine = automation.NewSOPEngine(sopStore, shellExecutor)

		alarmStore, aerr := automation.NewPostgresAlarmStore(database)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "daemon: alarm store open failed: %v\n", aerr)
		} else {
			daemonAlarm = automation.NewAlarmClockWithStore(
				alarmDispatchFire(daemonLedger),
				alarmStore,
			)
			daemonAlarm.Start()
			defer daemonAlarm.Stop()
			fmt.Printf("%s✓ automation engine started (%d alarms)%s\n", colorGreen, len(daemonAlarm.List()), colorReset)
			startedAuto = true
		}

		// Routine engine (§7.1 Layer 4). Persists across restarts.
		routineStore, rerr := automation.NewPostgresRoutineStore(database)
		if rerr == nil {
			routineEngine, rengErr := automation.NewRoutineEngineWithStore(shellExecutor, routineStore)
			if rengErr != nil {
				fmt.Fprintf(os.Stderr, "daemon: routine load: %v\n", rengErr)
			}
			if routineEngine != nil {
				daemonRoutines = routineEngine
				fmt.Printf("%s✓ routine engine started (%d routines)%s\n", colorGreen, len(routineEngine.List()), colorReset)
			} else {
				fmt.Fprintf(os.Stderr, "daemon: routine engine nil — routines disabled this run\n")
			}
		} else {
			fmt.Fprintf(os.Stderr, "daemon: routine store open failed: %v\n", rerr)
		}

		// Flow store for Task Flow durable resume (§7.1 Layer 7).
		if fs, ferr := agent.NewPostgresFlowStore(database); ferr == nil {
			daemonFlowStore = fs
		} else {
			fmt.Fprintf(os.Stderr, "daemon: flow store open failed: %v\n", ferr)
		}
	}

	if !startedCron && !startedAuto {
		return errors.New("daemon: nothing started — both cron and automation failed")
	}

	// Daily snapshot tick (master plan §4.20). Fire on start, then every 24h.
	dailySnapshotStore := session.NewPostgresStore(database)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dailySnapshotTick(dailySnapshotStore)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				dailySnapshotTick(dailySnapshotStore)
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

	// §7.1 Layer 6: push notification on terminal task transitions.
	// The ledger fires SetTerminalSink once per task as it lands in
	// Completed / Failed / Cancelled / Lost / TimedOut. We file an
	// AlertTaskCompleted record; the gateway hub (running in a
	// separate process) reads pending alerts and delivers them to
	// the user's bound channels.
	// D-6 / PCA-3: Wrap the terminal sink with OvenActions dispatch so
	// configured git_push, open_pr, deploy, notify_slack actions fire
	// automatically on task completion. Load user overrides to extract
	// any configured oven actions.
	sink := taskCompletionAlertSink()
	if uoPath, uoErr := config.UserOverridesPath(); uoErr == nil {
		if overrides, ovErr := config.LoadUserOverrides(uoPath); ovErr == nil && overrides != nil {
			if len(overrides.Advanced.Board.OvenActions) > 0 {
				sink = automation.WrapTerminalSinkWithOven(sink, overrides.Advanced.Board.OvenActions)
			}
		}
	}
	daemonLedger.SetTerminalSink(sink)

	// §4.19 SSE memory dashboard. Streams observation + alert events
	// over Server-Sent Events so users can build their own dashboard
	// or just `curl` the endpoint. Loopback-only by default; bearer
	// auth when OVERKILL_DASHBOARD_TOKEN is set.
	dashboard := journal.NewDashboardServer()
	dashboard.Listen = os.Getenv("OVERKILL_DASHBOARD_LISTEN")
	dashboard.Token = os.Getenv("OVERKILL_DASHBOARD_TOKEN")
	daemonDashboard = dashboard
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := dashboard.Run(ctx); err != nil && err != context.Canceled {
			fmt.Fprintf(os.Stderr, "daemon: dashboard: %v\n", err)
		}
	}()
	{
		addr := dashboard.Listen
		if addr == "" {
			addr = config.DefaultSSEDashboardAddr
		}
		fmt.Printf("%s✓ SSE dashboard armed at http://%s/dashboard/events%s\n", colorGreen, addr, colorReset)
	}

	// §7.1 Layer 3: SOP webhook trigger. External systems (CI hooks,
	// monitoring alerts, MQTT-to-HTTP relays) POST /sop/{id} on the
	// configured listen address to kick off a registered procedure.
	// Loopback-only by default; bearer-token auth when
	// OVERKILL_SOP_WEBHOOK_TOKEN is set.
	if daemonSOPEngine != nil {
		listen := os.Getenv("OVERKILL_SOP_WEBHOOK_LISTEN")
		webhook := &automation.SOPWebhookServer{
			Engine: daemonSOPEngine,
			Listen: listen,
			Token:  os.Getenv("OVERKILL_SOP_WEBHOOK_TOKEN"),
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := webhook.Run(ctx); err != nil && err != context.Canceled {
				fmt.Fprintf(os.Stderr, "daemon: sop webhook: %v\n", err)
			}
		}()
		addr := listen
		if addr == "" {
			addr = config.DefaultSOPWebhookAddr
		}
		fmt.Printf("%s✓ SOP webhook armed at http://%s%s\n", colorGreen, addr, colorReset)
	}

	// Behavior monitor + failhypo extraction ticker (paper #48).
	// Runs Wall 4 detectors and the failed-hypothesis regex pass over
	// today's journal entries every 5 minutes. Findings land in the
	// alert store so the next TUI boot surfaces them.
	behaviorTickerStart(ctx, &wg)

	// ImpossibleBench probe ticker (paper #8.4). Runs known-impossible
	// tasks against the agent every 15 minutes to detect cheating
	// (agent claiming success on tasks with no valid solution).
	// Uses a no-op responder in daemon mode — probes fire via cron/alarm
	// when an actual agent session is available. The ticker here ensures
	// the probe infrastructure is warmed and available.
	impossibleProbeStart(ctx, &wg)

	// PCA-8: Learning evolution engine — cold-path batch processing.
	// Clusters turn records, drafts skill improvements, judges success.
	// Runs every 6 hours. The learning store (hot-path) records every
	// correction; the engine (cold-path) turns those records into
	// actionable skill drafts.
	{
		homeDir, _ := daemonHomeDir()
		if homeDir != "" {
			recordDir := filepath.Join(homeDir, "learning", "records")
			skillDir := filepath.Join(homeDir, "skills")
			engine := learning.NewEngine(recordDir, skillDir, nil, nil, nil)
			wg.Add(1)
			go func() {
				defer wg.Done()
				// Run once on startup, then every 6 hours.
				learningEvolveTick(ctx, engine)
				ticker := time.NewTicker(6 * time.Hour)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						learningEvolveTick(ctx, engine)
					}
				}
			}()
			fmt.Printf("%s✓ learning evolution engine armed%s\n", colorGreen, colorReset)
		}
	}

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

// validateShellCommand blocks dangerous shell metacharacters that
// indicate command injection or chaining. Used as a safety net before
// any cron/SOP/alarm shell execution. Returns nil when safe.
func validateShellCommand(cmdStr string) error {
	// Block dangerous metacharacters for injection / chaining.
	if strings.ContainsAny(cmdStr, ";&|`$(){}[]") {
		return fmt.Errorf("shell command contains dangerous characters: %q", cmdStr)
	}
	// Block newlines — prevents multi-command injection via \n.
	if strings.Contains(cmdStr, "\n") || strings.Contains(cmdStr, "\r") {
		return fmt.Errorf("shell command contains newline: %q", cmdStr)
	}
	return nil
}

// shellOnFire is the default OnFire hook — runs the job's Command via /bin/sh.
// Output is logged and recorded into the daemon ledger so `overkill task list`
// can show what the daemon has done while the user was AFK.
func shellOnFire(j *cron.Job) error {
	if j.Command == "" {
		return errors.New("cron: job has no command")
	}
	if err := validateShellCommand(j.Command); err != nil {
		return fmt.Errorf("cron: %w", err)
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
	if err := validateShellCommand(action); err != nil {
		return "", fmt.Errorf("shell: %w", err)
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
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("daemon: find process: %w", err)
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		// On Windows, os.Interrupt is not supported for remote processes.
		// Fall back to Kill.
		if err2 := proc.Kill(); err2 != nil {
			return fmt.Errorf("daemon: kill: %w", err2)
		}
	}
	fmt.Printf("%sSent interrupt to daemon (pid %d)%s\n", colorGreen, pid, colorReset)
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

// pidIsRunning is implemented in daemon_unix.go and daemon_windows.go.

// learningEvolveTick fires one cold-path evolution cycle. Best-effort —
// errors are logged, never fatal. The engine accumulates turn records
// from the learning store and clusters them into skill drafts.
func learningEvolveTick(ctx context.Context, engine *learning.Engine) {
	if engine == nil {
		return
	}
	res, err := engine.RunColdPath(ctx, "", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon: learning evolve: %v\n", err)
		return
	}
	if res != nil && (res.ClustersFound > 0 || res.CandidateDrafts > 0) {
		fmt.Fprintf(os.Stderr, "%slearning evolve: %d records, %d clusters, %d candidates%s\n",
			colorDim, res.RecordsLoaded, res.ClustersFound, res.CandidateDrafts, colorReset)
	}
}
