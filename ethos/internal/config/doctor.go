package config

import (
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
	"github.com/rs/zerolog/log"
)

func Doctor() ([]string, error) {
	var fixes []string

	dir, err := ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("config: resolving config directory: %w", err)
	}
	fixes = append(fixes, checkAndCreateDir(dir, "config directory")...)

	configFile := fmt.Sprintf("%s/config.toml", dir)
	needsWrite := false

	cfg, loadErr := loadRaw(configFile)
	if loadErr != nil {
		if os.IsNotExist(loadErr) {
			log.Info().Msg("config file does not exist, creating default")
			cfg = Default()
			needsWrite = true
			fixes = append(fixes, "created default config file")
		} else {
			log.Warn().Err(loadErr).Msg("config file is corrupt, resetting to defaults")
			cfg = Default()
			needsWrite = true
			fixes = append(fixes, "reset corrupt config file to defaults")
		}
	}

	if cfg.Version != CurrentVersion {
		var migrateChanges []string
		cfg, migrateChanges, err = cfg.Migrate()
		if err != nil {
			return fixes, fmt.Errorf("config: migrating config: %w", err)
		}
		needsWrite = true
		fixes = append(fixes, migrateChanges...)
	}

	dataDir := cfg.Session.DataDir
	if dataDir == "" {
		homeDir, _ := os.UserHomeDir()
		if homeDir != "" {
			dataDir = fmt.Sprintf("%s/.overkill/data", homeDir)
		}
	}
	if dataDir != "" {
		fixes = append(fixes, checkAndCreateDir(dataDir, "data directory")...)
	}

	if cfg.Agent.DefaultProvider != "" {
		found := false
		for _, p := range cfg.Providers {
			if p.Name == cfg.Agent.DefaultProvider {
				found = true
				break
			}
		}
		if !found && len(cfg.Providers) > 0 {
			cfg.Agent.DefaultProvider = cfg.Providers[0].Name
			needsWrite = true
			fixes = append(fixes, fmt.Sprintf("fixed default_provider to %q (first available)", cfg.Providers[0].Name))
		}
	}

	if needsWrite {
		if err := cfg.Save(configFile); err != nil {
			return fixes, fmt.Errorf("config: writing fixed config: %w", err)
		}
	}

	for _, w := range cfg.Warnings() {
		log.Warn().Str("warning", w.String()).Msg("config diagnostic")
	}

	return fixes, nil
}

func loadRaw(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func checkAndCreateDir(path, label string) []string {
	var fixes []string
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if mkdirErr := os.MkdirAll(path, 0o755); mkdirErr != nil {
			log.Warn().Err(mkdirErr).Str("path", path).Msgf("cannot create %s", label)
		} else {
			fixes = append(fixes, fmt.Sprintf("created %s at %s", label, path))
		}
	} else if err == nil && !info.IsDir() {
		log.Warn().Str("path", path).Msgf("%s path exists but is not a directory", label)
	}
	return fixes
}
