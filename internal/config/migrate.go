package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/rs/zerolog/log"
)

func (c *Config) Migrate() (*Config, []string, error) {
	var changes []string

	switch {
	case c.Version == 0:
		changes = append(changes, "added version field (set to 1)")
		c.Version = CurrentVersion

		if c.Agent.Name == "" {
			c.Agent.Name = "Overkill"
			changes = append(changes, "set default agent name to \"Overkill\"")
		}
		// DefaultProvider / DefaultModel: only auto-set if there are
		// actually configured providers. Otherwise leave them blank so
		// the runtime can detect from env vars (OPENAI_API_KEY, etc.).
		// Writing "openai"/"gpt-4o" unconditionally was baking a
		// provider the user may never have set up into their config file.
		if c.Agent.DefaultProvider == "" && len(c.Providers) > 0 {
			c.Agent.DefaultProvider = c.Providers[0].Name
			changes = append(changes, fmt.Sprintf("set default provider to first configured: %q", c.Providers[0].Name))
		}
		if c.Agent.DefaultModel == "" && len(c.Providers) > 0 && len(c.Providers[0].Models) > 0 {
			c.Agent.DefaultModel = c.Providers[0].Models[0].ID
			changes = append(changes, fmt.Sprintf("set default model to first provider model: %q", c.Providers[0].Models[0].ID))
		}

		if c.Personality.Level == "" {
			c.Personality.Level = "subtle"
			changes = append(changes, "set default personality level to \"subtle\"")
		}
		if c.Personality.Language == "" {
			c.Personality.Language = "en"
			changes = append(changes, "set default personality language to \"en\"")
		}

		if c.Security.AutonomyLevel == "" {
			c.Security.AutonomyLevel = "supervised"
			changes = append(changes, "set default autonomy level to \"supervised\"")
		}
		if c.Security.DenyPatterns == nil {
			c.Security.DenyPatterns = []string{}
		}
		if c.Security.ForbiddenPaths == nil {
			c.Security.ForbiddenPaths = []string{}
		}
		if c.Security.MaxCommandLen == 0 {
			c.Security.MaxCommandLen = 4096
			changes = append(changes, "set default max command length to 4096")
		}

		if c.Session.DataDir == "" {
			homeDir, _ := os.UserHomeDir()
			if homeDir != "" {
				if runtime.GOOS == "windows" {
					localAppData := os.Getenv("LOCALAPPDATA")
					if localAppData != "" {
						c.Session.DataDir = filepath.Join(localAppData, "overkill", "data")
					} else {
						c.Session.DataDir = filepath.Join(homeDir, ".overkill", "data")
					}
				} else {
					c.Session.DataDir = filepath.Join(homeDir, ".overkill", "data")
				}
			}
			changes = append(changes, "set default data directory")
		}

		if c.Cost.RollingWindowHrs == 0 {
			c.Cost.RollingWindowHrs = 5
			changes = append(changes, "set default rolling window to 5 hours")
		}
		if c.Cost.WarnAtPercent == 0 {
			c.Cost.WarnAtPercent = 80
			changes = append(changes, "set default warn-at percent to 80")
		}

		if c.Compaction.SoftTriggerPercent == 0 {
			c.Compaction.SoftTriggerPercent = 50
			changes = append(changes, "set default soft trigger to 50%")
		}
		if c.Compaction.HardTriggerPercent == 0 {
			c.Compaction.HardTriggerPercent = 95
			changes = append(changes, "set default hard trigger to 95%")
		}
		if c.Compaction.PreserveMessages == 0 {
			c.Compaction.PreserveMessages = 20
			changes = append(changes, "set default preserve messages to 20")
		}
		if c.Compaction.MaxSummaryTokens == 0 {
			c.Compaction.MaxSummaryTokens = 2048
			changes = append(changes, "set default max summary tokens to 2048")
		}

		if c.Providers == nil {
			c.Providers = []ProviderConfig{}
		}

	case c.Version == CurrentVersion:
		// up to date

	default:
		log.Warn().Int("version", c.Version).Int("current", CurrentVersion).Msg("config version is from the future")
	}

	if len(changes) > 0 {
		path, err := ConfigPath()
		if err != nil {
			return c, changes, fmt.Errorf("config: getting config path for migration write: %w", err)
		}
		if err := c.Save(path); err != nil {
			return c, changes, fmt.Errorf("config: saving migrated config: %w", err)
		}
		log.Info().Strs("changes", changes).Msg("config migrated")
	}

	return c, changes, nil
}
