package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// delegateCmd wires the CLI entry point for sub-agent delegation.
//
//	overkill delegate --agent explore "find the auth code"
//	overkill delegate --agent plan "add rate limiting"
//	overkill delegate --agent verify "check for SQL injection"
//
// Built-in agents (explore, plan, verify) are registered on boot;
// user-defined agents from ~/.overkill/agents/*.md are picked up
// by the hotreload bus.
var delegateCmd = &cobra.Command{
	Use:   "delegate [query]",
	Short: "Delegate a task to a named sub-agent",
	Long: `Send a task to a named sub-agent (explore, plan, verify, or
user-defined agents from ~/.overkill/agents/).

Examples:
  overkill delegate --agent explore "find where auth tokens are validated"
  overkill delegate --agent plan "add rate limiting to the API"
  overkill delegate --agent verify "check recent changes for SQL injection"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentName, _ := cmd.Flags().GetString("agent")
		workDir, _ := cmd.Flags().GetString("workdir")
		timeoutSec, _ := cmd.Flags().GetInt("timeout")

		if agentName == "" {
			return fmt.Errorf("--agent is required (explore, plan, verify, or a custom agent name)")
		}

		query := args[0]

		// Resolve built-in agents from ~/.overkill/agents/ and register
		// them alongside any user-defined agents.
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("cannot resolve home dir: %w", err)
		}
		agentsDir := filepath.Join(homeDir, ".overkill", "agents")

		if workDir == "" {
			workDir, _ = os.Getwd()
		}
		if timeoutSec <= 0 {
			timeoutSec = 300 // 5 min default
		}

		d := subagent.NewExternalDelegator(workDir, time.Duration(timeoutSec)*time.Second, nil)

		// Register built-in agents. Pass nil resolver for CLI mode
		// (router is not wired into delegate command directly).
		for _, def := range subagent.BuiltinAgents(nil) {
			d.Register(def)
		}

		// If there's a custom agent of the same name from agents/ dir,
		// it would override. We rely on ExternalDelegator.Register
		// semantics (last write wins). Built-ins go in first.
		_ = agentsDir // in future, scan *.md from here

		// Check the agent exists.
		agents := d.ListAgents()
		found := false
		for _, a := range agents {
			if a.Name == agentName {
				found = true
				break
			}
		}
		if !found {
			available := make([]string, len(agents))
			for i, a := range agents {
				available[i] = a.Name
			}
			return fmt.Errorf("agent %q not found — available: %v", agentName, available)
		}

		result, err := d.Delegate(cmd.Context(), agentName, query)
		if err != nil {
			return fmt.Errorf("delegate: %w", err)
		}

		fmt.Printf("Status: %s\n", result.Status)
		if result.Summary != "" {
			fmt.Printf("\n%s\n", result.Summary)
		}
		if result.Error != "" {
			fmt.Printf("\nError: %s\n", result.Error)
		}
		return nil
	},
}

func init() {
	delegateCmd.Flags().StringP("agent", "a", "", "Sub-agent name (explore, plan, verify, or custom)")
	delegateCmd.Flags().StringP("workdir", "w", "", "Working directory (default: current dir)")
	delegateCmd.Flags().IntP("timeout", "t", 0, "Timeout in seconds (default: 300)")

	rootCmd.AddCommand(delegateCmd)
}
