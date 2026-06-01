package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Sahaj-Tech-ltd/overkill/internal/modules"

	"github.com/spf13/cobra"
)

var (
	modulesUpdateAll   bool
	modulesUpdateCheck bool
	modulesDryRun      bool
)

var modulesCmd = &cobra.Command{
	Use:   "modules",
	Short: "Manage bundled modules (skills, dependencies, plugins)",
	Long: `Track and update third-party modules bundled with Overkill.

Modules include skills (superpowers, caveman), system dependencies
(Postgres driver, unicode handlers), and TTS engines (edge-tts).

Updates are pulled from GitHub releases or Go module registries.`,
}

var modulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tracked modules",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := modules.NewManager(overkillHome())
		if err != nil {
			return err
		}

		mods := mgr.List()
		if len(mods) == 0 {
			fmt.Println("No modules tracked.")
			return nil
		}

		fmt.Print("## Tracked Modules\n\n")
		for _, mod := range mods {
			autoTag := ""
			if mod.AutoUpdate {
				autoTag = " [auto-update]"
			}
			fmt.Printf("**%s** (%s) %s → %s%s\n", mod.Name, mod.Type, mod.Source, mod.Version, autoTag)
			if mod.Description != "" {
				fmt.Printf("  %s\n", mod.Description)
			}
			fmt.Println()
		}

		return nil
	},
}

var modulesCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Check for available updates",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := modules.NewManager(overkillHome())
		if err != nil {
			return err
		}

		updates, skipped := mgr.CheckAll()
		if len(updates) == 0 && len(skipped) == 0 {
			fmt.Println("✅ All modules up to date.")
			return nil
		}
		if len(skipped) > 0 {
			fmt.Print("## Skipped (not yet implemented)\n\n")
			for _, s := range skipped {
				fmt.Printf("- %s\n", s)
			}
			fmt.Println()
		}
		if len(updates) > 0 {
			fmt.Print("## Available Updates\n\n")
			for name, info := range updates {
				fmt.Printf("- **%s**: %s\n", name, info)
			}
			fmt.Printf("\nRun `overkill modules update --all` to apply.\n\n")
		}

		return nil
	},
}

var modulesUpdateCmd = &cobra.Command{
	Use:   "update [module-name]",
	Short: "Update modules to latest versions",
	Long: `Update one or all tracked modules to their latest versions.

Without arguments, updates the specified module. Use --all to update
all modules with auto-update enabled. Use --check to preview without
applying changes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := modules.NewManager(overkillHome())
		if err != nil {
			return err
		}

		// --check mode: preview only.
		if modulesUpdateCheck {
			updates, skipped := mgr.CheckAll()
			if len(skipped) > 0 {
				fmt.Print("## Skipped (not yet implemented)\n\n")
				for _, s := range skipped {
					fmt.Printf("- %s\n", s)
				}
				fmt.Println()
			}
			if len(updates) == 0 {
				fmt.Println("✅ All modules up to date.")
				return nil
			}
			fmt.Print("## Would Update\n\n")
			for name, info := range updates {
				fmt.Printf("- **%s**: %s\n", name, info)
			}
			return nil
		}

		// --all mode: update everything with auto-update.
		if modulesUpdateAll {
			if modulesDryRun {
				fmt.Print("## Dry Run — would update the following:\n\n")
				for _, mod := range mgr.List() {
					if mod.AutoUpdate {
						latest, needsUpdate, _ := mgr.CheckForUpdates(mod.Name)
						if needsUpdate {
							fmt.Printf("- **%s**: %s → %s\n", mod.Name, mod.Version, latest)
						}
					}
				}
				return nil
			}

			results, err := mgr.UpdateAll()
			fmt.Println(modules.FormatUpdateReport(results))
			return err
		}

		// Single module update.
		if len(args) == 0 {
			return fmt.Errorf("specify a module name or use --all")
		}

		name := args[0]
		if modulesDryRun {
			latest, needsUpdate, err := mgr.CheckForUpdates(name)
			if err != nil {
				return err
			}
			if needsUpdate {
				mod := mgr.Get(name)
				fmt.Printf("Would update **%s**: %s → %s\n", name, mod.Version, latest)
			} else {
				fmt.Printf("**%s** is already up to date.\n", name)
			}
			return nil
		}

		result, err := mgr.Update(name)
		if err != nil {
			return err
		}

		if result.Updated {
			fmt.Printf("✅ Updated **%s**: %s → %s\n", name, result.FromVersion, result.ToVersion)
		} else if result.Skipped {
			fmt.Printf("⏭️ **%s**: %s\n", name, result.Reason)
		} else if result.Error != "" {
			fmt.Printf("❌ **%s**: %s\n", name, result.Error)
		}

		return nil
	},
}

func overkillHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback for restricted environments.
		if env := os.Getenv("OVERKILL_HOME"); env != "" {
			return env
		}
		home = "."
	}
	return filepath.Join(home, ".overkill")
}

func init() {
	modulesUpdateCmd.Flags().BoolVar(&modulesUpdateAll, "all", false, "Update all modules with auto-update enabled")
	modulesUpdateCmd.Flags().BoolVar(&modulesUpdateCheck, "check", false, "Check for updates without applying")
	modulesUpdateCmd.Flags().BoolVar(&modulesDryRun, "dry-run", false, "Preview changes without applying")

	modulesCmd.AddCommand(modulesListCmd)
	modulesCmd.AddCommand(modulesCheckCmd)
	modulesCmd.AddCommand(modulesUpdateCmd)
	rootCmd.AddCommand(modulesCmd)
}
