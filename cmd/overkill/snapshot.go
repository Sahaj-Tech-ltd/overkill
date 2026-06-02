package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <command>",
	Short: "Manage session snapshots and exports",
	Long: `Snapshot and export sessions. Snapshots are JSON exports stored in
~/.overkill/snapshots/<id>-<timestamp>.json.`,
}

var snapshotListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved snapshots",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := SnapshotDir()
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("%sno snapshots yet%s\n", colorDim, colorReset)
				return nil
			}
			return err
		}
		for _, e := range entries {
			info, _ := e.Info()
			fmt.Printf("  %s  %s\n", e.Name(), info.ModTime().Format(time.RFC3339))
		}
		return nil
	},
}

var snapshotExportCmd = &cobra.Command{
	Use:   "export <session-id>",
	Short: "Export a session snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		connString := os.Getenv("DATABASE_URL")
		if connString == "" && cfg != nil {
			connString = cfg.DatabaseURL
		}
		if connString == "" {
			return fmt.Errorf("no database configured — set DATABASE_URL or database_url in config.toml")
		}
		database, err := db.Open(connString)
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer database.Close()
		store := session.NewPostgresStore(database)

		s, err := store.Load(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("load session %s: %w", args[0], err)
		}
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal: %w", err)
		}
		dir := SnapshotDir()
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
		filename := fmt.Sprintf("%s-%s.json", s.ID, time.Now().Format("20060102-150405"))
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
		fmt.Printf("%s✓ exported to %s%s\n", colorGreen, path, colorReset)
		return nil
	},
}

func init() {
	snapshotCmd.AddCommand(snapshotListCmd, snapshotExportCmd)
	rootCmd.AddCommand(snapshotCmd)
}

// SnapshotDir returns the canonical snapshot directory.
func SnapshotDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".overkill", "snapshots")
}

// dailySnapshotTick is invoked by the daemon's internal scheduler.
// Best-effort — failures are logged, never fatal.
func dailySnapshotTick(store session.Store) {
	ctx := context.Background()
	sessions, err := store.List(ctx, session.ListOptions{Limit: 50})
	if err != nil {
		return
	}
	dir := SnapshotDir()
	_ = os.MkdirAll(dir, 0o750)
	for _, s := range sessions {
		data, _ := json.MarshalIndent(s, "", "  ")
		filename := fmt.Sprintf("%s-%s.json", s.ID, time.Now().Format("20060102-150405"))
		path := filepath.Join(dir, filename)
		_ = os.WriteFile(path, data, 0o600)
	}
}
