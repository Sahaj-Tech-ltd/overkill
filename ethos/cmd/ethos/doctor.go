package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/doctor"
	"github.com/Sahaj-Tech-ltd/ethos/internal/doctor/checks"
)

var (
	doctorJSON    bool
	doctorFailOn  string
	doctorNoColor bool
	doctorVerbose bool
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run the full self-test across every Ethos subsystem",
	Long: `doctor exercises every subsystem (config, providers, storage, MCP, LSP,
plugins, sync, ACP, tokenizer, tools, hooks, skills, filesystem, disk, etc.)
and prints a color-coded report. Use --json for machine output and --fail-on
to control the exit code in CI.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Resolve the config dir once. We deliberately re-derive it from the
		// loaded cfg path rather than calling config.ConfigDir again, so the
		// --config flag is honored.
		configDir := ""
		if resolvedCfgPath != "" {
			configDir = filepath.Dir(resolvedCfgPath)
		} else if d, err := config.ConfigDir(); err == nil {
			configDir = d
		}

		runner := doctor.NewRunner()
		deps := checks.DefaultDeps(cfg, configDir)
		checks.RegisterAll(runner, deps)

		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		summary := runner.Run(ctx)
		summary.Version = Version

		if doctorJSON {
			data, err := json.MarshalIndent(summary, "", "  ")
			if err != nil {
				return fmt.Errorf("encode json: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
		} else {
			doctor.PrettyPrint(cmd.OutOrStdout(), summary, doctor.PrettyOptions{
				NoColor: doctorNoColor || os.Getenv("NO_COLOR") != "",
				Verbose: doctorVerbose,
			})
		}

		switch doctorFailOn {
		case "warn":
			if summary.Counts.Fail+summary.Counts.Warn > 0 {
				os.Exit(1)
			}
		case "fail", "":
			if summary.Counts.Fail > 0 {
				os.Exit(1)
			}
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "emit JSON instead of pretty output")
	doctorCmd.Flags().StringVar(&doctorFailOn, "fail-on", "fail", "exit non-zero on 'warn' or 'fail' (default 'fail')")
	doctorCmd.Flags().BoolVar(&doctorNoColor, "no-color", false, "disable ANSI colors")
	doctorCmd.Flags().BoolVar(&doctorVerbose, "verbose", false, "show detail/fix on successful checks too")
	rootCmd.AddCommand(doctorCmd)
}
