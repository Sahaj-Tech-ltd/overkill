package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"

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
		if runtime.GOOS == "windows" {
			localAppData := os.Getenv("LOCALAPPDATA")
			if localAppData != "" {
				dataDir = filepath.Join(localAppData, "overkill", "data")
			} else {
				dataDir = filepath.Join(homeDir, ".overkill", "data")
			}
		} else {
			dataDir = filepath.Join(homeDir, ".overkill", "data")
		}
	}

	// Pick up DATABASE_URL from env as a reasonable default.
	// The user can override by setting database_url in config.toml.
	dbURL := os.Getenv("DATABASE_URL")

	cfg := &Config{
		Version: CurrentVersion,
		Agent: AgentConfig{
			Name:       "Overkill",
			MaxTurns:   0,
			SpecDriven: false,
			// DefaultProvider and DefaultModel are intentionally blank.
			// The runtime resolves providers via the Providers list or
			// env-var auto-detection (OPENAI_API_KEY, etc.). Hardcoding
			// "openai"/"gpt-4o" here bakes a provider the user may not
			// have set up into their config file.
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
			Strategy:           "auto",
			SoftTriggerPercent: 50,
			HardTriggerPercent: 95,
			PreserveMessages:   20,
			MaxSummaryTokens:   2048,
			UseLCM:             true,
		},
		UI: UIConfig{
			Animations: true,
		},
		DatabaseURL: dbURL,
		Gateways: GatewayConfig{
			MaxTextLen:        100_000,
			MaxImageBytes:     20_000_000,
			RateLimitPerMin:   10,
			UpdateEveryMs:     750,
			BackoffInitialSec: 1,
			BackoffMaxSec:     30,
		},
		LSP: LSPConfig{
			MaxMessageBytes: 32 * 1024 * 1024,
		},
		Cron: CronConfig{
			IdleWindowSec: 300,
		},
		Sync: SyncConfig{
			PushTimeoutSec: 30,
		},
	}
	if dbURL != "" {
		cfg.secretBindings = map[string]secretBinding{"database_url": {Reference: "${DATABASE_URL}", Resolved: dbURL}}
	}
	return cfg

}

func Load(path string) (*Config, error) {
	if path == "" {
		var err error
		path, err = ConfigPath()
		if err != nil {
			return nil, err
		}
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("config: resolve source path: %w", err)
	}
	path = absolutePath
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Warn().Str("path", path).Msg("config file not found, creating default")
			cfg := Default()
			cfg.sourcePath = path
			if writeErr := cfg.Save(path); writeErr != nil {
				return nil, fmt.Errorf("config: bootstrap default configuration: %w", writeErr)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("config: failed to read config file %s: %w", path, err)
	}

	// Start from defaults so TOML only overrides explicitly-set fields.
	// The old approach (zero-value + manual backfill) couldn't distinguish
	// "user set false" from "user didn't set this at all" — so use_lcm = false
	// was silently overridden to true. Defaults-first fixes this.
	cfg := *Default()
	cfg.sourcePath = path
	if err := unmarshalConfig(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: failed to parse config file %s: %w", path, err)
	}

	// Reject unenforced security guarantees before migration or persistence.
	if err := cfg.MCP.ValidateLimits(); err != nil {
		return nil, err
	}
	if err := cfg.Security.ValidateSupported(); err != nil {
		return nil, err
	}

	// Apply env var fallbacks for any unset fields.
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	}

	// Auto-migrate the config so version bumps apply defaults and
	// save the migrated file back to disk for next boot.
	if _, _, err := cfg.Migrate(); err != nil {
		log.Warn().Err(err).Msg("config: migration warning (using loaded config as-is)")
	}

	return &cfg, nil
}

// Enforce the provider collection shape independently of decoder coercions.
// In particular, an obsolete [providers.name] table must not silently become
// an empty provider list after a TOML dependency upgrade.
func unmarshalConfig(data []byte, cfg *Config) error {
	var shape map[string]any
	if err := toml.Unmarshal(data, &shape); err != nil {
		return err
	}
	if value, present := shape["providers"]; present {
		if value == nil || reflect.ValueOf(value).Kind() != reflect.Slice {
			return fmt.Errorf("providers must be an array of tables using [[providers]]")
		}
	}
	return toml.Unmarshal(data, cfg)
}

// PersistencePath returns the selected loaded profile, or the default for a new config.
func (c *Config) PersistencePath() (string, error) {
	if c.sourcePath != "" {
		return c.sourcePath, nil
	}
	return ConfigPath()
}

func (c *Config) Save(path string) error {
	if err := c.MCP.ValidateLimits(); err != nil {
		return err
	}
	if err := c.Security.ValidateSupported(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("config: creating directory %s: %w", dir, err)
	}

	persisted, err := c.persistenceCopy()
	if err != nil {
		return err
	}
	data, err := toml.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("config: marshaling toml: %w", err)
	}

	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("config: writing config file %s: %w", path, err)
	}

	return nil
}

func ConfigDir() (string, error) {
	// OVERKILL_HOME overrides the default config path.
	// Critical for restricted environments (containers, sub-agents, systemd)
	// where os.UserHomeDir() may be wrong or unavailable.
	if envDir := os.Getenv("OVERKILL_HOME"); envDir != "" {
		dir := filepath.Clean(envDir)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return "", fmt.Errorf("config: creating OVERKILL_HOME dir %s: %w", dir, err)
		}
		return dir, nil
	}

	var dir string
	if runtime.GOOS == "windows" {
		// Windows: use %LOCALAPPDATA%\overkill (non-roaming, machine-local)
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("config: getting home directory: %w (set OVERKILL_HOME to override)", err)
			}
			dir = filepath.Join(homeDir, ".overkill")
		} else {
			dir = filepath.Join(localAppData, "overkill")
		}
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: getting home directory: %w (set OVERKILL_HOME to override)", err)
		}
		dir = filepath.Join(homeDir, ".overkill")
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
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

// ThemesDir returns the path to the user's custom theme directory
// (~/.overkill/themes), creating it if it doesn't exist.
func ThemesDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	themesDir := filepath.Join(dir, "themes")
	if err := os.MkdirAll(themesDir, 0o750); err != nil {
		return "", fmt.Errorf("config: creating themes directory %s: %w", themesDir, err)
	}
	return themesDir, nil
}
