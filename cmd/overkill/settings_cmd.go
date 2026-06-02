package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/settings"
)

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "View and manage Overkill settings",
	Long: `The settings system uses reflection-based Go structs with struct tags
to define TOML-backed configuration groups. Each group is registered
via settings.Register(key, ptr) and populated from ~/.overkill/settings.toml.

Groups must be registered at init time by the package that owns them.
Use 'overkill settings' to verify LoadAll succeeds and defaults are applied.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgDir, err := config.ConfigDir()
		if err != nil {
			return fmt.Errorf("settings: cannot resolve config dir: %w", err)
		}

		// LoadAll reads ~/.overkill/settings.toml (if it exists) and
		// applies defaults + validates all registered groups.
		if err := settings.LoadAll(cfgDir); err != nil {
			fmt.Fprintf(os.Stderr, "settings: load: %v\n", err)
			return err
		}

		// Print success status.
		out := struct {
			Status string `json:"status"`
			Path   string `json:"path"`
		}{
			Status: "ok",
			Path:   cfgDir,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.AddCommand(settingsCmd)
}
