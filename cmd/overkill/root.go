package main

import (
	"fmt"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/features"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hotreload"
)

// Version is the build-time version string. Override with:
//
//	go build -ldflags="-X main.Version=vX.Y.Z" ./cmd/overkill
var Version = "0.1.0-dev"

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
	// hotReloadBus is the live-reload event bus, started in PersistentPreRunE
	// and consumed by run.go / tui.go for agent wiring.
	hotReloadBus *hotreload.Bus
	// featureManager holds the runtime feature flags loaded from ~/.overkill/features.toml.
	featureMgr *features.Manager
	// extensionsManager holds the unified extensions registry.
	extensionsMgr *extensions.Manager
)

var rootCmd = &cobra.Command{
	Use:     "overkill",
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

		// Bootstrap ~/.overkill/ with essential files (soul.md, CLAUDE.md, etc.)
		// on first run. Safe to call repeatedly — existing user-edited files
		// are never overwritten.
		if homeDir, err := config.ConfigDir(); err == nil {
			if err := BootstrapOverkillHome(homeDir); err != nil {
				log.Warn().Err(err).Msg("bootstrap failed, continuing anyway")
			}

			// §4.20: probe storage integrity on boot. A corrupt database
			// must NOT cause a silent cold-start (amnesia). Surface the
			// restore option before the agent loads.
			if res := probDBIntegrity(homeDir); res != nil && res.corrupt {
				log.Warn().
					Str("cause", res.cause).
					Bool("export_exists", res.exportExists).
					Msg("⚠️  DATABASE CORRUPT — run 'overkill doctor --check-db' for recovery options")
			}
		} else {
			log.Warn().Err(err).Msg("cannot resolve home dir, skipping bootstrap")
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

		// P1: hotreload bus — watches user.yaml and notifies subscribers.
		homeDir, _ := config.ConfigDir()
		if homeDir != "" {
			userYAML := filepath.Join(homeDir, "user.yaml")
			hotReloadBus = hotreload.New(hotreload.Paths{
				SkillsDir:       filepath.Join(homeDir, "skills"),
				AgentsDir:       filepath.Join(homeDir, "agents"),
				PluginsDir:      filepath.Join(homeDir, "plugins"),
				UserConfigFile:  userYAML,
			})
		}

		// P1: feature flags — load from ~/.overkill/features.toml if present.
		featureMgr = features.NewManager()
		if homeDir != "" {
			_ = featureMgr.LoadFromTOML(filepath.Join(homeDir, "features.toml"))
		}

		// P2: extensions manager — register known backends.
		extensionsMgr = extensions.NewManager()

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
	rootCmd.SetVersionTemplate(fmt.Sprintf("%soverkill %s%s\n", colorBold, Version, colorReset))
}
