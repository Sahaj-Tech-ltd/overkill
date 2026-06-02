package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/worktree"
)

// worktreeCmd surfaces the §8.5 parallel-agent worktree manager so
// users can inspect what subagents have checked out and reclaim
// stale trees after a crash. The subagent runtime drives Acquire /
// Release automatically; this is operator-side observability.
var worktreeCmd = &cobra.Command{
	Use:     "worktree",
	Short:   "Manage parallel-agent git worktrees (§8.5)",
	Aliases: []string{"wt"},
}

// workManager constructs a Manager rooted at the current working
// directory. We deliberately don't require a config file — worktree
// state lives entirely in the repo and on disk.
func workManager() (*worktree.Manager, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return worktree.NewManager(wd, ""), nil
}

var worktreeListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List active subagent worktrees",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := workManager()
		if err != nil {
			return err
		}
		// Reclaim first so we surface on-disk trees from prior runs.
		if err := m.Reclaim(); err != nil {
			return err
		}
		trees := m.List()
		if len(trees) == 0 {
			fmt.Printf("%sno active worktrees%s\n", colorDim, colorReset)
			return nil
		}
		for _, t := range trees {
			created := "(reclaimed)"
			if !t.CreatedAt.IsZero() {
				created = t.CreatedAt.Format(time.RFC3339)
			}
			fmt.Printf("  %s%s%s  %s%s%s\n", colorBold, t.TaskID, colorReset, colorDim, created, colorReset)
			fmt.Printf("    path:   %s\n", t.Path)
			fmt.Printf("    branch: %s\n", t.Branch)
		}
		return nil
	},
}

var worktreeReleaseCmd = &cobra.Command{
	Use:   "release <task-id>",
	Short: "Tear down a subagent worktree (idempotent)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		deleteBranch, _ := cmd.Flags().GetBool("delete-branch")
		m, err := workManager()
		if err != nil {
			return err
		}
		if err := m.Reclaim(); err != nil {
			return err
		}
		if err := m.Release(args[0], worktree.ReleaseOptions{
			Force:        force,
			DeleteBranch: deleteBranch,
		}); err != nil {
			return err
		}
		fmt.Printf("%s✓ released %s%s\n", colorGreen, args[0], colorReset)
		return nil
	},
}

var worktreePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Release every active worktree (use after a daemon crash)",
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		deleteBranch, _ := cmd.Flags().GetBool("delete-branch")
		m, err := workManager()
		if err != nil {
			return err
		}
		if err := m.Reclaim(); err != nil {
			return err
		}
		trees := m.List()
		if len(trees) == 0 {
			fmt.Printf("%snothing to prune%s\n", colorDim, colorReset)
			return nil
		}
		for _, t := range trees {
			if err := m.Release(t.TaskID, worktree.ReleaseOptions{Force: force, DeleteBranch: deleteBranch}); err != nil {
				fmt.Fprintf(os.Stderr, "release %s: %v\n", t.TaskID, err)
				continue
			}
			fmt.Printf("%s✓ released %s%s\n", colorGreen, t.TaskID, colorReset)
		}
		return nil
	},
}

func init() {
	worktreeCmd.AddCommand(worktreeListCmd)
	worktreeCmd.AddCommand(worktreeReleaseCmd)
	worktreeCmd.AddCommand(worktreePruneCmd)
	worktreeReleaseCmd.Flags().Bool("force", false, "force-remove even with uncommitted changes")
	worktreeReleaseCmd.Flags().Bool("delete-branch", false, "also delete the worktree's branch")
	worktreePruneCmd.Flags().Bool("force", false, "force-remove even with uncommitted changes")
	worktreePruneCmd.Flags().Bool("delete-branch", false, "also delete each worktree's branch")
	rootCmd.AddCommand(worktreeCmd)
}
