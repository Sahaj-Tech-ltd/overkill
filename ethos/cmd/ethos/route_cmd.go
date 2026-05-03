// Package main — `ethos route preview "<input>"` shows what the smart
// router would pick for a hypothetical message without sending it. Useful
// for tuning the classifier thresholds without having to round-trip a real
// turn.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/routing"
)

var routeCmd = &cobra.Command{
	Use:   "route",
	Short: "Inspect the smart router (master plan §5.2)",
}

var routePreviewCmd = &cobra.Command{
	Use:   "preview <input>",
	Short: "Show what model the router would pick for the given input",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text := joinArgs(args)
		_, modelName := resolveProvider()
		r := buildSmartRouter(modelName)
		if r == nil {
			fmt.Printf("%srouter unavailable (no providers in catalog)%s\n", colorYellow, colorReset)
			return nil
		}
		req := routing.RouteRequest{
			UserInput:       text,
			HistoryLength:   0,
			ToolCallCount:   0,
			HasAttachments:  false,
			CodeBlockCount:  0,
			EstimatedTokens: len(text) / 4,
		}
		res, err := r.Route(context.Background(), req)
		if err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(res, "", "  ")
		fmt.Fprintln(os.Stdout, string(raw))
		return nil
	},
}

func init() {
	routeCmd.AddCommand(routePreviewCmd)
	rootCmd.AddCommand(routeCmd)
}
