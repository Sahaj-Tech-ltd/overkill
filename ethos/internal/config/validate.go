package config

import (
	"fmt"
)

type Warning string

func (w Warning) String() string {
	return string(w)
}

func (c *Config) Validate() []error {
	var errs []error

	if c.Version <= 0 {
		errs = append(errs, fmt.Errorf("config: version must be >= 1, got %d", c.Version))
	}

	validAutonomy := map[string]bool{
		"readonly":   true,
		"supervised": true,
		"full":       true,
	}
	if !validAutonomy[c.Security.AutonomyLevel] {
		errs = append(errs, fmt.Errorf("config: security.autonomy_level must be one of [readonly, supervised, full], got %q", c.Security.AutonomyLevel))
	}

	validPersonality := map[string]bool{
		"subtle": true,
		"witty":  true,
		"full":   true,
		"off":    true,
	}
	if !validPersonality[c.Personality.Level] {
		errs = append(errs, fmt.Errorf("config: personality.level must be one of [subtle, witty, full, off], got %q", c.Personality.Level))
	}

	if c.Agent.DefaultProvider != "" {
		found := false
		for _, p := range c.Providers {
			if p.Name == c.Agent.DefaultProvider {
				found = true
				break
			}
		}
		if !found && len(c.Providers) > 0 {
			errs = append(errs, fmt.Errorf("config: agent.default_provider %q not found in providers list", c.Agent.DefaultProvider))
		}
	}

	for i, p := range c.Providers {
		if p.Name == "" {
			errs = append(errs, fmt.Errorf("config: providers[%d].name is required", i))
		}
		if p.Type == "" {
			errs = append(errs, fmt.Errorf("config: providers[%d].type is required", i))
		}
		validTypes := map[string]bool{
			"openai":     true,
			"anthropic":  true,
			"gemini":     true,
			"ollama":     true,
			"openrouter": true,
			"custom":     true,
		}
		if p.Type != "" && !validTypes[p.Type] {
			errs = append(errs, fmt.Errorf("config: providers[%d].type %q is not a valid provider type", i, p.Type))
		}
		if p.Type != "ollama" && p.APIKey == "" {
			errs = append(errs, fmt.Errorf("config: providers[%d].api_key is required for non-ollama provider %q", i, p.Type))
		}
	}

	if c.Cost.DailyLimitUSD < 0 {
		errs = append(errs, fmt.Errorf("config: cost.daily_limit_usd must be non-negative, got %.2f", c.Cost.DailyLimitUSD))
	}
	if c.Cost.PerTaskLimitUSD < 0 {
		errs = append(errs, fmt.Errorf("config: cost.per_task_limit_usd must be non-negative, got %.2f", c.Cost.PerTaskLimitUSD))
	}

	if c.Compaction.SoftTriggerPercent < 0 || c.Compaction.SoftTriggerPercent > 100 {
		errs = append(errs, fmt.Errorf("config: compaction.soft_trigger_percent must be 0-100, got %d", c.Compaction.SoftTriggerPercent))
	}
	if c.Compaction.HardTriggerPercent < 0 || c.Compaction.HardTriggerPercent > 100 {
		errs = append(errs, fmt.Errorf("config: compaction.hard_trigger_percent must be 0-100, got %d", c.Compaction.HardTriggerPercent))
	}

	return errs
}

func (c *Config) Warnings() []Warning {
	var warns []Warning

	if len(c.Providers) == 0 {
		warns = append(warns, Warning("no providers configured; agent will not be able to make LLM calls"))
	}

	if c.Cost.DailyLimitUSD == 0 {
		warns = append(warns, Warning("cost.daily_limit_usd is not set; usage costs will not be bounded"))
	}

	hasOllama := false
	hasOther := false
	for _, p := range c.Providers {
		if p.Type == "ollama" {
			hasOllama = true
		} else {
			hasOther = true
		}
	}
	if hasOllama && !hasOther {
		warns = append(warns, Warning("only ollama provider configured; some capabilities may be limited without a cloud provider"))
	}

	if c.Security.AutonomyLevel == "full" && !c.Security.SandboxEnabled {
		warns = append(warns, Warning("full autonomy without sandbox enabled; consider enabling sandbox for safety"))
	}

	return warns
}
