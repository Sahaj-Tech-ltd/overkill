// Package main — `ethos daemon` boots background services: cron scheduler,
// automation alarm clock, routine engine. Foreground process by default;
// runs until SIGINT/SIGTERM. PID file at ~/.ethos/daemon.pid for `stop`/
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

	"github.com/Sahaj-Tech-ltd/ethos/internal/automation"
	"github.com/Sahaj-Tech-ltd/ethos/internal/cron"
)

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
	rootCmd.AddCommand(daemonCmd)
}

func daemonHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ethos")
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
			return fmt.Errorf("daemon already running (pid %d) — use 'ethos daemon stop' first", pid)
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

	if autoDB, err := badger.Open(badger.DefaultOptions(autoDir).WithLoggingLevel(badger.ERROR)); err == nil {
		defer autoDB.Close()
		store := automation.NewBadgerSOPStore(autoDB)
		_ = automation.NewSOPEngine(store, shellExecutor)
		alarm := automation.NewAlarmClock(func(a *automation.Alarm) error {
			fmt.Printf("%s🔔 alarm: %s%s\n", colorYellow, a.Name, colorReset)
			return nil
		})
		alarm.Start()
		defer alarm.Stop()
		fmt.Printf("%s✓ automation engine started (%d alarms)%s\n", colorGreen, len(alarm.List()), colorReset)
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

	fmt.Printf("%sethos daemon running (pid %d) — Ctrl-C to stop%s\n", colorBlue, os.Getpid(), colorReset)
	<-ctx.Done()
	fmt.Println()
	fmt.Printf("%sethos daemon shutting down...%s\n", colorYellow, colorReset)
	wg.Wait()
	return nil
}

// shellOnFire is the default OnFire hook — runs the job's Command via /bin/sh.
// Output is logged but not persisted (the cron Job's RunCount/FailureCount
// updates live inside the scheduler).
func shellOnFire(j *cron.Job) error {
	if j.Command == "" {
		return errors.New("cron: job has no command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", j.Command).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cron %s] FAILED: %v\n%s\n", j.Name, err, string(out))
		return err
	}
	fmt.Printf("[cron %s] ok\n", j.Name)
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
	uptime := time.Duration(0)
	if info != nil {
		uptime = time.Since(info.ModTime())
	}
	fmt.Printf("%sdaemon running — pid=%d uptime=%s%s\n", colorGreen, pid, uptime.Round(time.Second), colorReset)
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
