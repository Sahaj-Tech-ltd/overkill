package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

var (
	checkpointDir       string
	checkpointKeep      int
	checkpointSessionID string
)

var checkpointCmd = &cobra.Command{
	Use:   "checkpoint",
	Short: "Snapshot and restore file state before destructive operations",
	Long: `Overkill checkpoints capture file contents before the agent runs
destructive tool calls (fs_write, fs_delete, git, rm, etc.).

Use "overkill checkpoint list" to see saved snapshots.
Use "overkill checkpoint restore <id>" to roll back to a snapshot.`,
}

var checkpointListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved checkpoints",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getCheckpointManager()
		if err != nil {
			return err
		}
		mans, err := mgr.List(checkpointSessionID)
		if err != nil {
			return fmt.Errorf("listing checkpoints: %w", err)
		}
		if len(mans) == 0 {
			fmt.Println("No checkpoints found.")
			return nil
		}
		fmt.Printf("%-40s  %-20s  %s\n", "ID", "CREATED", "REASON")
		fmt.Println("----------------------------------------  --------------------  ------")
		for _, m := range mans {
			reason := m.Reason
			if reason == "" {
				reason = "-"
			}
			if len(reason) > 40 {
				reason = reason[:37] + "..."
			}
			t := m.CreatedAt.Local().Format("2006-01-02 15:04:05")
			fmt.Printf("%-40s  %-20s  %s\n", m.ID, t, reason)
		}
		return nil
	},
}

var checkpointRestoreCmd = &cobra.Command{
	Use:   "restore <checkpoint-id>",
	Short: "Restore files from a checkpoint snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		mgr, err := getCheckpointManager()
		if err != nil {
			return err
		}
		skipped, err := mgr.Restore(id)
		if err != nil {
			return fmt.Errorf("restoring checkpoint %s: %w", id, err)
		}
		if len(skipped) > 0 {
			fmt.Println("Skipped (files too large to restore):")
			for _, s := range skipped {
				fmt.Printf("  %s\n", s)
			}
		}
		fmt.Printf("Restored checkpoint %s\n", id)
		return nil
	},
}

var checkpointPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove old checkpoints beyond the retention limit",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := getCheckpointManager()
		if err != nil {
			return err
		}
		mans, err := mgr.List("")
		if err != nil {
			return fmt.Errorf("listing checkpoints: %w", err)
		}
		if len(mans) <= checkpointKeep {
			fmt.Printf("Nothing to prune (%d checkpoints, keep limit %d)\n", len(mans), checkpointKeep)
			return nil
		}
		// Prune oldest first
		toRemove := mans[checkpointKeep:]
		for _, m := range toRemove {
			dir := filepath.Join(checkpointDir, m.ID)
			if err := os.RemoveAll(dir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not remove %s: %v\n", m.ID, err)
			} else {
				fmt.Printf("Removed %s (%s)\n", m.ID, m.CreatedAt.Local().Format(time.RFC3339))
			}
		}
		fmt.Printf("Pruned %d checkpoints, %d remaining\n", len(toRemove), checkpointKeep)
		return nil
	},
}

func getCheckpointManager() (*checkpoint.Manager, error) {
	dir := checkpointDir
	if dir == "" {
		homeDir, err := config.ConfigDir()
		if err != nil {
			return nil, fmt.Errorf("resolving config dir: %w", err)
		}
		dir = filepath.Join(homeDir, "checkpoints")
	}
	keep := checkpointKeep
	if keep <= 0 {
		keep = 50
	}
	return checkpoint.NewManager(dir, keep)
}

func init() {
	checkpointCmd.PersistentFlags().StringVar(&checkpointDir, "dir", "", "Checkpoint directory (default: ~/.overkill/checkpoints)")
	checkpointCmd.PersistentFlags().IntVar(&checkpointKeep, "keep", 50, "Max checkpoints to retain per session")
	checkpointListCmd.Flags().StringVar(&checkpointSessionID, "session", "", "Filter by session ID")
	checkpointCmd.AddCommand(checkpointListCmd)
	checkpointCmd.AddCommand(checkpointRestoreCmd)
	checkpointCmd.AddCommand(checkpointPruneCmd)
	rootCmd.AddCommand(checkpointCmd)
}
