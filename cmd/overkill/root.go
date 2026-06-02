package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/features"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hotreload"
	"github.com/Sahaj-Tech-ltd/overkill/internal/settings"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
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

		// setup and its parent commands don't require a config file to
		// exist — they create one. Skip config loading entirely for those
		// paths so a fresh install can bootstrap without a pre-existing
		// config.toml.
		isSetup := cmd.Name() == "setup" || (cmd.Parent() != nil && cmd.Parent().Name() == "config" && cmd.Name() == "setup")
		if isSetup {
			homeDir, _ := config.ConfigDir()
			if homeDir != "" {
				// Still bootstrap so soul.md and CLAUDE.md are created.
				if err := BootstrapOverkillHome(homeDir); err != nil {
					log.Warn().Err(err).Msg("bootstrap failed, continuing anyway")
				}
			}
			return nil
		}

		path := cfgPath
		if path == "" {
			p, err := config.ConfigPath()
			if err != nil {
				return fmt.Errorf("config: failed to resolve config path: %w", err)
			}
			path = p
		}
		resolvedCfgPath = path

		loaded, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("config: failed to load: %w", err)
		}
		cfg = loaded

		if err := cfg.ResolveSecrets(); err != nil {
			return fmt.Errorf("config: secrets resolution failed: %w", err)
		}

		// Bootstrap ~/.overkill/ with essential files (soul.md, CLAUDE.md, etc.)
		// on first run. Safe to call repeatedly — existing user-edited files
		// are never overwritten.
		if homeDir, err := config.ConfigDir(); err == nil {
			if err := BootstrapOverkillHome(homeDir); err != nil {
				log.Warn().Err(err).Msg("bootstrap failed, continuing anyway")
			}

			// §4.20: storage integrity check removed — Postgres backend
			// doesn't have BadgerDB-style silent corruption.
			log.Debug().Msg("storage backend: Postgres")
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
				SkillsDir:      filepath.Join(homeDir, "skills"),
				AgentsDir:      filepath.Join(homeDir, "agents"),
				PluginsDir:     filepath.Join(homeDir, "plugins"),
				UserConfigFile: userYAML,
			})
		}

		// P1: feature flags — load from ~/.overkill/features.toml if present.
		featureMgr = features.NewManager()
		if homeDir != "" {
			_ = featureMgr.LoadFromTOML(filepath.Join(homeDir, "features.toml"))
		}

		// P2: extensions manager — register known backends.
		extensionsMgr = extensions.NewManager()

		// Wire skills backend into the extensions manager so skill
		// toggling works through the unified extensions surface.
		if homeDir != "" {
			userSkillDir := filepath.Join(homeDir, "skills")
			bundledSkillDir := os.Getenv("OVERKILL_BUNDLED_SKILLS")
			loader := skills.NewLoader(bundledSkillDir, userSkillDir)
			if loaded, err := loader.LoadAll(); err == nil && len(loaded) > 0 {
				reg := skills.NewRegistry()
				for i := range loaded {
					_ = reg.Register(&loaded[i])
				}
				extensionsMgr.AddBackend(extensions.NewSkillsBackend(reg))
				log.Debug().Int("count", len(loaded)).Msg("extensions: skills backend registered")
			} else if err != nil {
				log.Warn().Err(err).Msg("extensions: skills load failed, backend not registered")
			}
		}

		// Apply settings defaults from ~/.overkill/settings.toml.
		// LoadAll is a no-op when no groups are registered, but when
		// packages register themselves, their defaults and validation
		// run here on every boot.
		if homeDir != "" {
			if err := settings.LoadAll(homeDir); err != nil {
				log.Warn().Err(err).Msg("settings load failed, continuing")
			}
		}

		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		// If no provider is configured, route to setup wizard instead of
		// opening the TUI (which needs a working LLM backend).
		if cfg != nil && len(cfg.Providers) == 0 && cfg.Agent.DefaultProvider == "" {
			fmt.Println()
			fmt.Printf("%s%s⚡ No provider configured.%s\n", colorBold, colorYellow, colorReset)
			fmt.Println()
			fmt.Println("  Let's set one up. I'll walk you through it.")
			fmt.Println()
			return runSetup(cmd, args)
		}
		return runInkTUI(cmd, args)
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgPath, "config", "", "config file path")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "suppress non-error output")
	rootCmd.SetVersionTemplate(fmt.Sprintf("%soverkill %s%s\n", colorBold, Version, colorReset))
}
