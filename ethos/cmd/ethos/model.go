package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var modelCmd = &cobra.Command{
	Use:   "model",
	Short: "Manage models",
}

var modelListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List available models from all providers",
	Aliases: []string{"ls"},
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(cfg.Providers) == 0 {
			fmt.Printf("%sNo providers configured%s\n", colorYellow, colorReset)
			return nil
		}

		for _, p := range cfg.Providers {
			fmt.Printf("%s%s%s (%s)\n", colorBold, p.Name, colorReset, p.Type)
			if len(p.Models) == 0 {
				fmt.Printf("  %s(no models configured)%s\n", colorDim, colorReset)
			}
			for _, m := range p.Models {
				fmt.Printf("  %s• %s%s (%s)\n", colorBlue, m.Name, colorReset, m.ID)
			}
		}

		return nil
	},
}

var modelSetCmd = &cobra.Command{
	Use:   "set <provider/model>",
	Short: "Set default model",
	Args:  cobra.ExactArgs(1),
	Example: "  ethos model set openai/gpt-4o\n" +
		"  ethos model set anthropic/claude-sonnet-4-20250514",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("%sModel selection not yet implemented (requested: %s)%s\n", colorYellow, args[0], colorReset)
		return nil
	},
}

func init() {
	modelCmd.AddCommand(modelListCmd)
	modelCmd.AddCommand(modelSetCmd)

	rootCmd.AddCommand(modelCmd)
}
