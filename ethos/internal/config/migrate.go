package config

import (
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
)

func (c *Config) Migrate() (*Config, []string, error) {
	var changes []string

	switch {
	case c.Version == 0:
		changes = append(changes, "added version field (set to 1)")
		c.Version = CurrentVersion

		if c.Agent.Name == "" {
			c.Agent.Name = "Ethos"
			changes = append(changes, "set default agent name to \"Ethos\"")
		}
		if c.Agent.DefaultProvider == "" {
			c.Agent.DefaultProvider = "openai"
			changes = append(changes, "set default provider to \"openai\"")
		}
		if c.Agent.DefaultModel == "" {
			c.Agent.DefaultModel = "gpt-4o"
			changes = append(changes, "set default model to \"gpt-4o\"")
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
				c.Session.DataDir = homeDir + "/.overkill/data"
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

		fallthrough

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
