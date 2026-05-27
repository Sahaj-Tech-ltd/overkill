package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

func Default() *Config {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}
	dataDir := ""
	if homeDir != "" {
		dataDir = filepath.Join(homeDir, ".overkill", "data")
	}

	return &Config{
		Version: CurrentVersion,
		Agent: AgentConfig{
			Name:            "Overkill",
			DefaultProvider: "openai",
			DefaultModel:    "gpt-4o",
			MaxTurns:        0,
			SpecDriven:      false,
		},
		Providers: []ProviderConfig{},
		Personality: PersonalityConfig{
			Level:    "subtle",
			Language: "en",
		},
		Security: SecurityConfig{
			AutonomyLevel:  "supervised",
			DenyPatterns:   []string{},
			ForbiddenPaths: []string{},
			MaxCommandLen:  4096,
			SandboxEnabled: false,
		},
		Session: SessionConfig{
			AutoTitle:   true,
			MaxSessions: 0,
			DataDir:     dataDir,
		},
		Cost: CostConfig{
			DailyLimitUSD:    0,
			PerTaskLimitUSD:  0,
			RollingWindowHrs: 5,
			WarnAtPercent:    80,
		},
		Compaction: CompactionConfig{
			SoftTriggerPercent: 50,
			HardTriggerPercent: 95,
			PreserveMessages:   20,
			MaxSummaryTokens:   2048,
			UseLCM:             true,
		},
		UI: UIConfig{
			Animations: true,
		},
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn().Str("path", path).Msg("config file not found, creating default")
			cfg := Default()
			if writeErr := cfg.Save(path); writeErr != nil {
				log.Warn().Err(writeErr).Msg("failed to write default config")
			}
			return cfg, nil
		}
		log.Warn().Err(err).Str("path", path).Msg("failed to read config file, using defaults")
		return Default(), nil
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("failed to parse config file, using defaults")
		return Default(), nil
	}

	// Apply env var fallbacks for any unset fields.
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	}

	return &cfg, nil
}

func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: creating directory %s: %w", dir, err)
	}

	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: marshaling toml: %w", err)
	}

	if err := atomicfile.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("config: writing config file %s: %w", path, err)
	}

	return nil
}

func ConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: getting home directory: %w", err)
	}

	dir := filepath.Join(homeDir, ".overkill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("config: creating config directory %s: %w", dir, err)
	}

	return dir, nil
}

func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "config.toml"), nil
}
