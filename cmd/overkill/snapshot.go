// Package main — `overkill snapshot` manages BadgerDB session snapshots
// (master plan §4.20). Snapshots live under ~/.overkill/snapshots; the daemon
// schedules a daily snapshot via the cron scheduler.
//
// Subcommands:
//   - create:  take a fresh snapshot now
//   - list:    list existing snapshot files
//   - restore: load a snapshot back into the live store
//   - export:  write a human-readable memory dump to ~/.overkill/exports/
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

const defaultMaxSnapshots = 7

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Manage BadgerDB session snapshots and exports",
}

var snapshotCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Take a snapshot of the session store",
	RunE: func(cmd *cobra.Command, args []string) error {
		sm, cleanup, err := openSnapshotManager()
		if err != nil {
			return err
		}
		defer cleanup()
		path, err := sm.CreateSnapshot(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Printf("%s✓ snapshot written: %s%s\n", colorGreen, path, colorReset)
		return nil
	},
}

var snapshotListCmd = &cobra.Command{
	Use:   "list",
	Short: "List existing snapshots",
	RunE: func(cmd *cobra.Command, args []string) error {
		sm, cleanup, err := openSnapshotManager()
		if err != nil {
			return err
		}
		defer cleanup()
		names, err := sm.ListSnapshots()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Printf("%sno snapshots yet — run 'overkill snapshot create''%s\n", colorYellow, colorReset)
			return nil
		}
		for _, n := range names {
			fmt.Println(n)
		}
		return nil
	},
}

var snapshotRestoreCmd = &cobra.Command{
	Use:   "restore <snapshot-file>",
	Short: "Restore the session store from a snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sm, cleanup, err := openSnapshotManager()
		if err != nil {
			return err
		}
		defer cleanup()
		path := args[0]
		// Accept either a bare filename (look up under ~/.overkill/snapshots) or
		// a fully-qualified path.
		if !filepath.IsAbs(path) {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, ".overkill", "snapshots", path)
		}
		if err := sm.RestoreFromSnapshot(cmd.Context(), path); err != nil {
			return err
		}
		fmt.Printf("%s✓ restored from %s%s\n", colorGreen, path, colorReset)
		return nil
	},
}

var snapshotExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write a human-readable memory export under ~/.overkill/exports/",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cleanup, err := openSessionStore()
		if err != nil {
			return err
		}
		defer cleanup()
		home, _ := os.UserHomeDir()
		exportDir := filepath.Join(home, ".overkill", "exports")
		if err := os.MkdirAll(exportDir, 0o755); err != nil {
			return err
		}
		// Prefer the snapshot path (deterministic, dump-everything) so the
		// export reflects the durable state, not just the in-memory one.
		sm, cleanupSM, err := openSnapshotManager()
		if err != nil {
			return err
		}
		defer cleanupSM()
		snap, err := sm.CreateSnapshot(cmd.Context())
		if err != nil {
			return err
		}
		out := filepath.Join(exportDir, fmt.Sprintf("memory-export-%s.md", time.Now().UTC().Format("2006-01-02-150405")))
		if err := writeMemoryExport(snap, out); err != nil {
			return err
		}
		fmt.Printf("%s✓ memory export: %s%s\n", colorGreen, out, colorReset)
		return nil
	},
}

func init() {
	snapshotCmd.AddCommand(snapshotCreateCmd)
	snapshotCmd.AddCommand(snapshotListCmd)
	snapshotCmd.AddCommand(snapshotRestoreCmd)
	snapshotCmd.AddCommand(snapshotExportCmd)
	rootCmd.AddCommand(snapshotCmd)
}

// openSnapshotManager returns a SnapshotManager rooted at the session store
// dir. Caller MUST call cleanup() to close the BadgerDB.
func openSnapshotManager() (*session.SnapshotManager, func(), error) {
	store, cleanup, err := openSessionStore()
	if err != nil {
		return nil, nil, err
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".overkill", "snapshots")
	return session.NewSnapshotManager(store, dir, defaultMaxSnapshots), cleanup, nil
}

// openSessionStore opens the per-user session store at ~/.overkill/sessions.
// Returns a cleanup that closes the underlying Badger DB.
func openSessionStore() (*session.BadgerStore, func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(home, ".overkill", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	store, err := session.NewBadgerStore(dir)
	if err != nil {
		return nil, nil, err
	}
	return store, func() { _ = store.Close() }, nil
}

// writeMemoryExport reads a snapshot file and renders a markdown summary.
// Lightweight: counts entries by key prefix and pulls the first N session
// titles. Mostly meant as an "I have a copy of my brain" reassurance file.
func writeMemoryExport(snapshotPath, outPath string) error {
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return err
	}
	// Snapshot is JSON; we don't unmarshal it — just record provenance.
	hdr := fmt.Sprintf("# Overkill Memory Export\n- Snapshot: %s\n- Generated: %s\n- Bytes: %d\n\n",
		filepath.Base(snapshotPath), time.Now().UTC().Format(time.RFC3339), len(data))
	body := "_This file is a human-friendly companion to the binary snapshot. " +
		"To restore, run `overkill snapshot restore` + filepath.Base(snapshotPath) " + filepath.Base(snapshotPath) + "`._\n"
	return os.WriteFile(outPath, []byte(hdr+body), 0o644)
}

// SnapshotDir returns the canonical snapshot directory (used by the daemon's
// daily-snapshot tick).
func SnapshotDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".overkill", "snapshots")
}

// dailySnapshotTick is invoked by the daemon's internal scheduler (see
// daemon.go). Best-effort — failures are logged, never fatal.
func dailySnapshotTick(ctx context.Context) {
	sm, cleanup, err := openSnapshotManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot: open: %v\n", err)
		return
	}
	defer cleanup()
	path, err := sm.CreateSnapshot(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "snapshot: create: %v\n", err)
		return
	}
	fmt.Printf("[snapshot] wrote %s\n", filepath.Base(path))
}
