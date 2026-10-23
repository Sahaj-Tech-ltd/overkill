package main

import (
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/spf13/cobra"
)

var extensionsCmd = &cobra.Command{Use: "extensions", Short: "List installed extensions and their runtime state", RunE: func(cmd *cobra.Command, args []string) error {
	if extensionsMgr == nil {
		return fmt.Errorf("extensions manager not initialized")
	}
	items, err := extensionsMgr.List()
	if err != nil {
		return err
	}
	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		if items == nil {
			items = []extensions.Extension{}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(items)
	}
	for _, item := range items {
		state := "inactive"
		if item.Enabled {
			state = "enabled"
		}
		if runtime := item.Metadata["runtime_state"]; runtime != "" {
			state = runtime
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s %s — %s\n", item.Kind, state, item.Name, item.Description)
		if !item.CanToggle && item.UnsupportedReason != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", item.UnsupportedReason)
		}
	}
	return nil
}}

func init() {
	extensionsCmd.PersistentFlags().Bool("json", false, "Emit structured extension inventory")
	for _, verb := range []string{"enable", "disable"} {
		command := &cobra.Command{Use: verb + " <kind> <id>", Short: verb + " a supported extension", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			if extensionsMgr == nil {
				return fmt.Errorf("extensions manager not initialized")
			}
			if verb == "enable" {
				return extensionsMgr.Enable(extensions.Kind(args[0]), args[1])
			}
			return extensionsMgr.Disable(extensions.Kind(args[0]), args[1])
		}}
		extensionsCmd.AddCommand(command)
	}
	rootCmd.AddCommand(extensionsCmd)
}
