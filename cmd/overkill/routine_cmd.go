package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
)

// routineCmd groups the §7.1 Layer 4 routine management
// subcommands. Routines are event→action rules with a cooldown;
// the daemon delivers events to a persistent engine, and these
// commands manipulate that engine's store directly (so the CLI
// works even when the daemon isn't running).
var routineCmd = &cobra.Command{
	Use:   "routine",
	Short: "Manage automation routines (event → action with cooldown)",
}

// openRoutineStore opens the same Badger DB the daemon uses and
// wraps it in a routine store. The caller must Close the returned
// *badger.DB. Returns (nil, nil, err) if the DB can't open.
func openRoutineStore() (*automation.BadgerRoutineStore, *badger.DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(home, ".overkill", "automation")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	db, err := badger.Open(badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR))
	if err != nil {
		return nil, nil, fmt.Errorf("open automation db: %w", err)
	}
	return automation.NewBadgerRoutineStore(db), db, nil
}

var routineListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List registered routines",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		store, db, err := openRoutineStore()
		if err != nil {
			return err
		}
		defer db.Close()
		rs, err := store.Load()
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			fmt.Printf("%sno routines registered%s\n", colorDim, colorReset)
			return nil
		}
		for _, r := range rs {
			status := "enabled"
			color := colorGreen
			if !r.Enabled {
				status = "disabled"
				color = colorDim
			}
			fmt.Printf("  %s%s%s  %s[%s]%s  %s%s → %s%s\n",
				colorBold, r.ID, colorReset,
				color, status, colorReset,
				colorDim, r.Trigger, r.Action, colorReset,
			)
			if r.Name != "" && r.Name != r.ID {
				fmt.Printf("    %s\n", r.Name)
			}
			meta := fmt.Sprintf("cooldown=%s  fires=%d", r.Cooldown, r.FireCount)
			if !r.LastFired.IsZero() {
				meta += "  last=" + r.LastFired.Format(time.RFC3339)
			}
			fmt.Printf("    %s%s%s\n", colorDim, meta, colorReset)
		}
		return nil
	},
}

var routineAddCmd = &cobra.Command{
	Use:   "add <trigger> <action>",
	Short: "Register a routine (event → action)",
	Long: `Adds a routine that runs <action> whenever an agent event
matching <trigger> fires. Cooldown defaults to 5 minutes; override
with --cooldown=DURATION (e.g. 30s, 10m, 1h).

Examples:
  overkill routine add tool_call_blocked "echo 'security event'"
  overkill routine add recovery "/usr/local/bin/notify-slack" --cooldown=1m`,
	Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		trigger := args[0]
		action := strings.Join(args[1:], " ")
		cooldownStr, _ := cmd.Flags().GetString("cooldown")
		name, _ := cmd.Flags().GetString("name")
		idFlag, _ := cmd.Flags().GetString("id")

		cooldown := 5 * time.Minute
		if cooldownStr != "" {
			d, err := time.ParseDuration(cooldownStr)
			if err != nil {
				return fmt.Errorf("invalid --cooldown: %w", err)
			}
			cooldown = d
		}
		id := idFlag
		if id == "" {
			id = strings.TrimSpace(trigger) + "-" + uuid.New().String()[:8]
		}
		r := &automation.Routine{
			ID:       id,
			Name:     name,
			Trigger:  trigger,
			Action:   action,
			Cooldown: cooldown,
			Enabled:  true,
		}

		store, db, err := openRoutineStore()
		if err != nil {
			return err
		}
		defer db.Close()
		if err := store.Save(r); err != nil {
			return err
		}
		fmt.Printf("%s✓ routine %s registered (%s → %s, cooldown %s)%s\n",
			colorGreen, r.ID, r.Trigger, r.Action, r.Cooldown, colorReset)
		fmt.Printf("%sRestart the daemon for it to pick up the new routine: overkill daemon stop && overkill daemon start%s\n",
			colorDim, colorReset)
		return nil
	},
}

var routineRmCmd = &cobra.Command{
	Use:     "rm <id>",
	Short:   "Delete a routine by ID",
	Aliases: []string{"remove", "delete"},
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, db, err := openRoutineStore()
		if err != nil {
			return err
		}
		defer db.Close()
		if err := store.Delete(args[0]); err != nil {
			return err
		}
		fmt.Printf("%s✓ routine %s deleted%s\n", colorGreen, args[0], colorReset)
		return nil
	},
}

var routineEnableCmd = &cobra.Command{
	Use:   "enable <id>",
	Short: "Enable a routine",
	Args:  cobra.ExactArgs(1),
	RunE:  toggleRoutine(true),
}

var routineDisableCmd = &cobra.Command{
	Use:   "disable <id>",
	Short: "Disable a routine without deleting it",
	Args:  cobra.ExactArgs(1),
	RunE:  toggleRoutine(false),
}

func toggleRoutine(enabled bool) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		store, db, err := openRoutineStore()
		if err != nil {
			return err
		}
		defer db.Close()
		rs, err := store.Load()
		if err != nil {
			return err
		}
		var found *automation.Routine
		for _, r := range rs {
			if r.ID == args[0] {
				found = r
				break
			}
		}
		if found == nil {
			return fmt.Errorf("routine %q not found", args[0])
		}
		found.Enabled = enabled
		if err := store.Save(found); err != nil {
			return err
		}
		state := "enabled"
		if !enabled {
			state = "disabled"
		}
		fmt.Printf("%s✓ routine %s %s%s\n", colorGreen, found.ID, state, colorReset)
		return nil
	}
}

func init() {
	routineCmd.AddCommand(routineListCmd)
	routineCmd.AddCommand(routineAddCmd)
	routineCmd.AddCommand(routineRmCmd)
	routineCmd.AddCommand(routineEnableCmd)
	routineCmd.AddCommand(routineDisableCmd)
	routineAddCmd.Flags().String("cooldown", "5m", "minimum time between fires (e.g. 30s, 10m)")
	routineAddCmd.Flags().String("name", "", "human-readable name")
	routineAddCmd.Flags().String("id", "", "explicit ID (default: derived from trigger)")
	rootCmd.AddCommand(routineCmd)
}
