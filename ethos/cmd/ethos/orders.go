// Package main — `ethos orders add|rm|list|toggle` manages standing orders
// (master plan §7.1). Stored at ~/.ethos/standing-orders.jsonl; loaded by
// the TUI's per-turn context provider.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/automation"
)

var ordersCmd = &cobra.Command{
	Use:   "orders",
	Short: "Manage persistent standing orders",
}

var ordersListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show every standing order (enabled or not)",
	RunE: func(cmd *cobra.Command, args []string) error {
		o, err := openOrders()
		if err != nil {
			return err
		}
		all := o.All()
		if len(all) == 0 {
			fmt.Printf("%sno standing orders yet — `ethos orders add \"...\"`%s\n", colorYellow, colorReset)
			return nil
		}
		for _, so := range all {
			marker := " "
			if so.Enabled {
				marker = "✓"
			}
			fmt.Printf("%s [%s] %s\n", marker, so.ID, so.Text)
		}
		return nil
	},
}

var ordersAddCmd = &cobra.Command{
	Use:   "add <text>",
	Short: "Add a new standing order",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		o, err := openOrders()
		if err != nil {
			return err
		}
		text := joinArgs(args)
		so, err := o.Add(text)
		if err != nil {
			return err
		}
		fmt.Printf("%s✓ added: [%s] %s%s\n", colorGreen, so.ID, so.Text, colorReset)
		return nil
	},
}

var ordersRemoveCmd = &cobra.Command{
	Use:     "rm <id>",
	Aliases: []string{"remove", "delete"},
	Short:   "Remove a standing order",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		o, err := openOrders()
		if err != nil {
			return err
		}
		if err := o.Remove(args[0]); err != nil {
			return err
		}
		fmt.Printf("%s✓ removed %s%s\n", colorGreen, args[0], colorReset)
		return nil
	},
}

var ordersToggleCmd = &cobra.Command{
	Use:   "toggle <id>",
	Short: "Enable/disable a standing order",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		o, err := openOrders()
		if err != nil {
			return err
		}
		// Toggle: find current state and flip.
		var current bool
		var found bool
		for _, so := range o.All() {
			if so.ID == args[0] {
				current = so.Enabled
				found = true
				break
			}
		}
		if !found {
			return errors.New("not found")
		}
		if err := o.SetEnabled(args[0], !current); err != nil {
			return err
		}
		fmt.Printf("%s✓ %s -> enabled=%v%s\n", colorGreen, args[0], !current, colorReset)
		return nil
	},
}

func openOrders() (*automation.OrdersFile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return automation.NewOrdersFile(filepath.Join(home, ".ethos", "standing-orders.jsonl"))
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func init() {
	ordersCmd.AddCommand(ordersListCmd)
	ordersCmd.AddCommand(ordersAddCmd)
	ordersCmd.AddCommand(ordersRemoveCmd)
	ordersCmd.AddCommand(ordersToggleCmd)
	rootCmd.AddCommand(ordersCmd)
}
