package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
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
	Example: "  overkill model set openai/gpt-4o\n" +
		"  overkill model set anthropic/claude-sonnet-4-20250514",
	RunE: func(cmd *cobra.Command, args []string) error {
		parts := strings.SplitN(args[0], "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("expected provider/model (e.g. openai/gpt-4o), got %q", args[0])
		}
		provName, modelID := parts[0], parts[1]

		cfg.Agent.DefaultProvider = provName
		cfg.Agent.DefaultModel = modelID

		path := resolvedCfgPath
		if path == "" {
			p, err := config.ConfigPath()
			if err != nil {
				return err
			}
			path = p
		}
		if err := cfg.Save(path); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("%s✓ default model set to %s/%s%s\n", colorGreen, provName, modelID, colorReset)
		return nil
	},
}

func init() {
	modelCmd.AddCommand(modelListCmd)
	modelCmd.AddCommand(modelSetCmd)

	rootCmd.AddCommand(modelCmd)
}
