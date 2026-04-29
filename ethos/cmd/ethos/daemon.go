package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the background daemon",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start background daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("%sDaemon start not yet implemented%s\n", colorYellow, colorReset)
		return nil
	},
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("%sDaemon stop not yet implemented%s\n", colorYellow, colorReset)
		return nil
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("%sDaemon status not yet implemented%s\n", colorYellow, colorReset)
		return nil
	},
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)

	rootCmd.AddCommand(daemonCmd)
}
