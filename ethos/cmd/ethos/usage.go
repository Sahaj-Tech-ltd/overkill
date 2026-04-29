package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var usageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show token usage and cost breakdown",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("%sUsage tracking not yet implemented%s\n", colorYellow, colorReset)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(usageCmd)
}
