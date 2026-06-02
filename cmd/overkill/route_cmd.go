// Package main — `overkill route preview "<input>"` shows what the smart
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

	"github.com/Sahaj-Tech-ltd/overkill/internal/routing"
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

var routeFamilyCmd = &cobra.Command{
	Use:   "family <name>",
	Short: "Pick the cheapest non-deprecated model in a family (§5.2 family-aware routing)",
	Long: `Resolve a family name (e.g. "claude-opus", "gpt-5") to the cheapest
non-deprecated model in that family using the local TOML catalog.

Useful for "use cheapest Claude" style requests. Returns the model ID
and provider, plus the failover chain (members of the family sorted by
output-token cost) so you can see what the router would try next.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		family := args[0]
		_, modelName := resolveProvider()
		r := buildSmartRouter(modelName)
		if r == nil {
			fmt.Printf("%srouter unavailable (no catalog loaded)%s\n", colorYellow, colorReset)
			return nil
		}
		id, provider, err := r.ModelInFamily(family)
		if err != nil {
			fmt.Printf("%s%v%s\n", colorRed, err, colorReset)
			return nil
		}
		chain := r.FailoverInFamily(family)
		out := struct {
			Family   string   `json:"family"`
			Pick     string   `json:"pick"`
			Provider string   `json:"provider"`
			Failover []string `json:"failover"`
		}{family, id, provider, chain}
		raw, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(os.Stdout, string(raw))
		return nil
	},
}

func init() {
	routeCmd.AddCommand(routePreviewCmd)
	routeCmd.AddCommand(routeFamilyCmd)
	rootCmd.AddCommand(routeCmd)
}
