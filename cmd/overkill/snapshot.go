package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <command>",
	Short: "Manage session snapshots and exports",
	Long: `Snapshot and export sessions. Snapshots are JSON exports stored in
the active profile’s snapshots directory (OVERKILL_HOME when set).`,
}

var snapshotListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved snapshots",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := SnapshotDir()
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("%sno snapshots yet%s\n", colorDim, colorReset)
				return nil
			}
			return err
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				return err
			}
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
		store, err := session.NewPostgresStore(database)
		if err != nil {
			return fmt.Errorf("initialize session store: %w", err)
		}

		s, err := store.Load(cmd.Context(), args[0])
		if err != nil {
			return fmt.Errorf("load session %s: %w", args[0], err)
		}
		path, err := writeSessionSnapshot(s)
		if err != nil {
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
func SnapshotDir() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "snapshots"), nil
}

// Unique private files avoid overwriting a prior export in the same second.
func writeSessionSnapshot(s *session.Session) (string, error) {
	if s == nil || s.ID == "" || s.ID == "." || s.ID == ".." || strings.ContainsAny(s.ID, "/\\\x00") {
		return "", fmt.Errorf("invalid snapshot session ID")
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal snapshot: %w", err)
	}
	dir, err := SnapshotDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, s.ID+"-"+time.Now().Format("20060102-150405")+"-*.json")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// dailySnapshotTick is invoked by the daemon's internal scheduler.
func dailySnapshotTick(store session.Store) {
	ctx := context.Background()
	sessions, err := store.List(ctx, session.ListOptions{Limit: 50})
	if err != nil {
		slog.Warn("list sessions for snapshot", "error", err)
		return
	}
	for _, s := range sessions {
		if _, err := writeSessionSnapshot(s); err != nil {
			slog.Warn("save session snapshot", "error", err)
		}
	}
}
