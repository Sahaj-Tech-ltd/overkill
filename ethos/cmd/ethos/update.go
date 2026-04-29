package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for updates and self-update",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("%sSelf-update not yet implemented (current: %s)%s\n", colorYellow, Version, colorReset)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
