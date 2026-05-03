package main

import (
	"fmt"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
)

const Version = "0.1.0-dev"

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

var (
	cfgPath         string
	verbose         bool
	quiet           bool
	cfg             *config.Config
	resolvedCfgPath string
)

var rootCmd = &cobra.Command{
	Use:     "ethos",
	Short:   "The vibe-coding agent that actually has discipline.",
	Version: Version,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if verbose {
			zerolog.SetGlobalLevel(zerolog.DebugLevel)
		}
		if quiet {
			zerolog.SetGlobalLevel(zerolog.ErrorLevel)
		}

		path := cfgPath
		if path == "" {
			p, err := config.ConfigPath()
			if err != nil {
				log.Warn().Err(err).Msg("failed to resolve config path, using defaults")
				cfg = config.Default()
				return nil
			}
			path = p
		}
		resolvedCfgPath = path

		loaded, err := config.Load(path)
		if err != nil {
			log.Warn().Err(err).Msg("failed to load config, using defaults")
			cfg = config.Default()
			return nil
		}
		cfg = loaded

		if err := cfg.ResolveSecrets(); err != nil {
			log.Warn().Err(err).Msg("failed to resolve secrets")
		}

		for _, e := range cfg.Validate() {
			log.Warn().Err(e).Msg("config validation")
		}

		for _, w := range cfg.Warnings() {
			log.Warn().Str("warning", w.String()).Msg("config")
		}

		migrated, changes, err := cfg.Migrate()
		if err != nil {
			log.Warn().Err(err).Msg("config migration failed")
		} else if len(changes) > 0 {
			cfg = migrated
			for _, c := range changes {
				log.Info().Str("change", c).Msg("config migrated")
			}
		}

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUI(cmd, args)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "", "config file path")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-error output")
	rootCmd.SetVersionTemplate(fmt.Sprintf("%sethos %s%s\n", colorBold, Version, colorReset))
}
