// Package main — `overkill task list|get` inspects the in-process daemon
// ledger (master plan §7.1). The ledger is per-process — outside the daemon
// it shows only what this CLI invocation has recorded; inside `daemon start`
// it accumulates everything the daemon has done.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Inspect the background task ledger",
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent tasks",
	RunE: func(cmd *cobra.Command, args []string) error {
		all := daemonLedger.List()
		if len(all) == 0 {
			fmt.Printf("%sno tasks recorded in this process%s\n", colorYellow, colorReset)
			fmt.Printf("%s(start the daemon — `overkill daemon start` — to accumulate tasks)%s\n", colorYellow, colorReset)
			return nil
		}
		raw, _ := json.MarshalIndent(all, "", "  ")
		fmt.Fprintln(os.Stdout, string(raw))
		return nil
	},
}

var taskGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show a single task by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, ok := daemonLedger.Get(args[0])
		if !ok {
			return fmt.Errorf("task %q not found", args[0])
		}
		raw, _ := json.MarshalIndent(t, "", "  ")
		fmt.Fprintln(os.Stdout, string(raw))
		return nil
	},
}

func init() {
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskGetCmd)
	rootCmd.AddCommand(taskCmd)
}
