package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var extensionsCmd = &cobra.Command{
	Use:   "extensions",
	Short: "List installed extensions",
	RunE: func(cmd *cobra.Command, args []string) error {
		if extensionsMgr == nil {
			return fmt.Errorf("extensions manager not initialized")
		}
		all, err := extensionsMgr.List()
		if err != nil {
			return err
		}
		for _, e := range all {
			status := "disabled"
			if e.Enabled {
				status = "enabled"
			}
			fmt.Printf("[%s] %-4s %s — %s\n", e.Kind, status, e.Name, e.Description)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(extensionsCmd)
}
