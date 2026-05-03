// Package main — `ethos usage [--session ID] [--days N]` prints a cost
// breakdown across every recorded turn (master plan §4.5).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/cost"
)

var (
	usageJSON      bool
	usageSessionID string
	usageDays      int
)

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show token usage and cost (master plan §4.5)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ct, cleanup, err := openCostTracker()
		if err != nil {
			return err
		}
		defer cleanup()

		ctx := cmd.Context()
		end := time.Now().UTC()
		start := end.AddDate(0, 0, -usageDays)
		opts := cost.UsageOptions{
			SessionID: usageSessionID,
			StartTime: start,
			EndTime:   end,
		}
		report, err := ct.Usage(ctx, opts)
		if err != nil {
			return err
		}

		if usageJSON {
			raw, _ := json.MarshalIndent(report, "", "  ")
			fmt.Fprintln(os.Stdout, string(raw))
			return nil
		}
		printUsageReport(report, usageDays)
		return nil
	},
}

func openCostTracker() (cost.Tracker, func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Join(home, ".ethos", "costs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	costCfg := config.CostConfig{}
	if cfg != nil {
		costCfg = cfg.Cost
	}
	t, err := cost.NewBadgerTracker(dir, costCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("usage: %w", err)
	}
	return t, func() { _ = t.Close() }, nil
}

func printUsageReport(r *cost.UsageReport, days int) {
	if r == nil {
		fmt.Printf("%sno usage recorded%s\n", colorYellow, colorReset)
		return
	}
	fmt.Printf("%s── usage (last %d day%s) ──%s\n", colorBlue, days, plural(days), colorReset)
	fmt.Printf("total: $%.4f  (in=%d out=%d cached=%d, %d call(s))\n",
		r.Summary.TotalUSD, r.Summary.InputTokens, r.Summary.OutputTokens, r.Summary.CachedTokens, r.Summary.RequestCount)
	if len(r.ByModel) > 0 {
		fmt.Println()
		fmt.Println("by model:")
		for m, s := range r.ByModel {
			fmt.Printf("  %-40s $%.4f  (in=%d out=%d, %d calls)\n", m, s.TotalUSD, s.InputTokens, s.OutputTokens, s.RequestCount)
		}
	}
	if len(r.ByProvider) > 0 {
		fmt.Println()
		fmt.Println("by provider:")
		for p, s := range r.ByProvider {
			fmt.Printf("  %-20s $%.4f  (%d calls)\n", p, s.TotalUSD, s.RequestCount)
		}
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func init() {
	usageCmd.Flags().BoolVar(&usageJSON, "json", false, "emit JSON")
	usageCmd.Flags().StringVar(&usageSessionID, "session", "", "filter by session ID")
	usageCmd.Flags().IntVar(&usageDays, "days", 7, "look back N days (default 7)")
	rootCmd.AddCommand(usageCmd)
}
