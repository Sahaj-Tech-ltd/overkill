package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls/monitor"
)

// monitorCmd surfaces the behavioral session monitor (Wall 4, paper
// #48). The detectors live in internal/walls/monitor; this command
// wires them to the on-disk journal so the user can run a scan
// on-demand. A future iteration runs this on a daemon ticker.
var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Behavioral session monitor (paper #48 / Wall 4)",
}

var monitorScanCmd = &cobra.Command{
	Use:   "scan [session-id]",
	Short: "Scan a session for OpenAI-taxonomy behavior categories",
	Long: `Walks the flight-recorder entries for the given session (default:
"default") and runs every heuristic detector — circumvention,
deception, concealing uncertainty, reward hacking, unauthorized
data transfer. Findings are grouped and printed; the journal is
not modified.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("monitor: resolve home: %w", err)
		}
		jdir := filepath.Join(home, ".overkill", "journal")

		sid := "default"
		if len(args) == 1 {
			sid = args[0]
		}

		r := journal.NewFlightRecorder(jdir, sid)
		entries, err := r.ReadSession(sid)
		if err != nil {
			return fmt.Errorf("monitor: read session %s: %w", sid, err)
		}
		if len(entries) == 0 {
			fmt.Printf("%sno journal entries for session %q%s\n", colorDim, sid, colorReset)
			return nil
		}

		findings := monitor.Scan(entries)
		if len(findings) == 0 {
			fmt.Printf("%s✓ no behavioral findings across %d entries%s\n", colorGreen, len(entries), colorReset)
			return nil
		}
		fmt.Print(monitor.FormatAlert(findings))
		return nil
	},
}

var monitorScanDayCmd = &cobra.Command{
	Use:   "scan-day",
	Short: "Scan today's flight-recorder entries (all sessions)",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("monitor: resolve home: %w", err)
		}
		jdir := filepath.Join(home, ".overkill", "journal")

		r := journal.NewFlightRecorder(jdir, "")
		entries, err := r.ReadDay(time.Now())
		if err != nil {
			return fmt.Errorf("monitor: read today: %w", err)
		}
		if len(entries) == 0 {
			fmt.Printf("%sno journal entries for today%s\n", colorDim, colorReset)
			return nil
		}

		findings := monitor.Scan(entries)
		if len(findings) == 0 {
			fmt.Printf("%s✓ no behavioral findings across %d entries%s\n", colorGreen, len(entries), colorReset)
			return nil
		}
		fmt.Print(monitor.FormatAlert(findings))
		return nil
	},
}

func init() {
	monitorCmd.AddCommand(monitorScanCmd)
	monitorCmd.AddCommand(monitorScanDayCmd)
	rootCmd.AddCommand(monitorCmd)
}
