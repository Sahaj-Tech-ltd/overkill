package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	modelOverride  string
	providerOvrride string
	noPersonality  bool
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the agent loop",
	RunE:  runAgent,
}

func runAgent(cmd *cobra.Command, args []string) error {
	if modelOverride != "" {
		fmt.Printf("model override: %s\n", modelOverride)
	}
	if providerOvrride != "" {
		fmt.Printf("provider override: %s\n", providerOvrride)
	}
	if noPersonality {
		fmt.Println("personality disabled")
	}

	fmt.Printf("%sAgent loop not yet implemented%s\n", colorYellow, colorReset)
	return nil
}

func init() {
	runCmd.Flags().StringVar(&modelOverride, "model", "", "override default model")
	runCmd.Flags().StringVar(&providerOvrride, "provider", "", "override default provider")
	runCmd.Flags().BoolVar(&noPersonality, "no-personality", false, "disable personality engine")

	rootCmd.AddCommand(runCmd)
}
