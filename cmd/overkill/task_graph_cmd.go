package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tasks"
)

// threadCmd surfaces the §8.3 cross-session task graph for operators.
// The agent mutates the store via typed tools; this is for human
// inspection + manual cleanup.
var threadCmd = &cobra.Command{
	Use:     "thread",
	Short:   "Cross-session task graph (§8.3)",
	Aliases: []string{"threads"},
}

func openThreadStore() (*tasks.Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return tasks.NewStore(filepath.Join(home, ".overkill", "tasks")), nil
}

var threadListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List tasks",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		openOnly, _ := cmd.Flags().GetBool("open")
		s, err := openThreadStore()
		if err != nil {
			return err
		}
		var list []*tasks.Task
		if openOnly {
			list, err = s.OpenTasks()
		} else {
			list, err = s.All()
		}
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Printf("%sno tasks%s\n", colorDim, colorReset)
			return nil
		}
		for _, t := range list {
			color := colorYellow
			switch t.Status {
			case tasks.StatusShipped:
				color = colorGreen
			case tasks.StatusAbandoned:
				color = colorDim
			}
			age := time.Since(t.CreatedAt).Round(time.Hour)
			fmt.Printf("  %s%s%s  %s%-12s%s  %s%s ago%s\n",
				colorBold, t.ID[:8], colorReset,
				color, t.Status, colorReset,
				colorDim, age, colorReset,
			)
			fmt.Printf("    %s\n", t.Intent)
			if len(t.Commits) > 0 {
				fmt.Printf("    %scommits: %s%s\n", colorDim, strings.Join(t.Commits, ", "), colorReset)
			}
			if t.Notes != "" {
				fmt.Printf("    %snotes: %s%s\n", colorDim, t.Notes, colorReset)
			}
		}
		return nil
	},
}

var threadShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a single task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openThreadStore()
		if err != nil {
			return err
		}
		// Allow short-prefix lookup so users don't have to paste the
		// full UUID. Resolves to the first task whose ID has the
		// given prefix.
		all, err := s.All()
		if err != nil {
			return err
		}
		prefix := args[0]
		for _, t := range all {
			if strings.HasPrefix(t.ID, prefix) {
				fmt.Printf("%s%s%s\n", colorBold, t.ID, colorReset)
				fmt.Printf("  status: %s\n", t.Status)
				fmt.Printf("  intent: %s\n", t.Intent)
				fmt.Printf("  session: %s\n", t.SessionID)
				fmt.Printf("  created: %s\n", t.CreatedAt.Format(time.RFC3339))
				fmt.Printf("  updated: %s\n", t.UpdatedAt.Format(time.RFC3339))
				if !t.ResolvedAt.IsZero() {
					fmt.Printf("  resolved: %s\n", t.ResolvedAt.Format(time.RFC3339))
				}
				if len(t.Commits) > 0 {
					fmt.Printf("  commits:\n")
					for _, c := range t.Commits {
						fmt.Printf("    - %s\n", c)
					}}
				if t.Notes != "" {
					fmt.Printf("  notes: %s\n", t.Notes)
				}
				return nil
			}
		}
		return fmt.Errorf("task %q not found", prefix)
	},
}

var threadCloseCmd = &cobra.Command{
	Use:   "close <id>",
	Short: "Close a task (default: shipped; use --abandoned to mark unfinished)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		abandoned, _ := cmd.Flags().GetBool("abandoned")
		s, err := openThreadStore()
		if err != nil {
			return err
		}
		// Prefix-resolve.
		all, err := s.All()
		if err != nil {
			return err
		}
		prefix := args[0]
		for _, t := range all {
			if strings.HasPrefix(t.ID, prefix) {
				status := tasks.StatusShipped
				if abandoned {
					status = tasks.StatusAbandoned
				}
				_, err := s.SetStatus(t.ID, status)
				if err != nil {
					return err
				}
				fmt.Printf("%s✓ %s → %s%s\n", colorGreen, t.ID[:8], status, colorReset)
				return nil
			}
		}
		return fmt.Errorf("task %q not found", prefix)
	},
}

func init() {
	threadCmd.AddCommand(threadListCmd)
	threadCmd.AddCommand(threadShowCmd)
	threadCmd.AddCommand(threadCloseCmd)
	threadListCmd.Flags().Bool("open", false, "only show non-terminal tasks")
	threadCloseCmd.Flags().Bool("abandoned", false, "mark as abandoned instead of shipped")
	rootCmd.AddCommand(threadCmd)
}
