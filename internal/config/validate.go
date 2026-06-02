package config

import (
	"fmt"
	"os"

	"github.com/rs/zerolog/log"
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
		"safe":       true,
		"yolo":       true,
		"auto":       true,
	}
	if !validAutonomy[c.Security.AutonomyLevel] {
		errs = append(errs, fmt.Errorf("config: security.autonomy_level must be one of [readonly, supervised, full, safe, yolo, auto], got %q", c.Security.AutonomyLevel))
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
			"deepseek":   true,
			"ollama":     true,
			"openrouter": true,
			"groq":       true,
			"xai":        true,
			"mistral":    true,
			"togetherai": true,
			"perplexity": true,
			"deepinfra":  true,
			"cerebras":   true,
			"fireworks":  true,
			"bedrock":    true,
			"vertex":     true,
			"azure":      true,
			"copilot":    true,
			"custom":     true,
		}
		if p.Type != "" && !validTypes[p.Type] {
			// Provider type not in our known list — the factory now
			// auto-discovers from models.dev catalog. Warn but don't
			// block validation (B109): unknown types may still work
			// via auto-discovery.
			log.Warn().Str("type", p.Type).Msg("config: provider type not in known list; will attempt auto-discovery from models.dev")
		}
		if p.Type != "ollama" && p.Type != "bedrock" && p.Type != "vertex" && p.APIKey == "" {
			errs = append(errs, fmt.Errorf("config: providers[%d].api_key is required for non-ollama provider %q", i, p.Type))
		}
	}

	if c.Cost.DailyLimitUSD < 0 {
		errs = append(errs, fmt.Errorf("config: cost.daily_limit_usd must be non-negative, got %.2f", c.Cost.DailyLimitUSD))
	}
	if c.Cost.PerTaskLimitUSD < 0 {
		errs = append(errs, fmt.Errorf("config: cost.per_task_limit_usd must be non-negative, got %.2f", c.Cost.PerTaskLimitUSD))
	}

	if c.Agent.MaxOutputTokens < 0 {
		errs = append(errs, fmt.Errorf("config: agent.max_output_tokens must be >= 0, got %d", c.Agent.MaxOutputTokens))
	}
	if c.Agent.MaxTurns < 0 {
		errs = append(errs, fmt.Errorf("config: agent.max_turns must be >= 0, got %d", c.Agent.MaxTurns))
	}
	if c.Gateways.BackoffInitialSec < 0 {
		errs = append(errs, fmt.Errorf("config: gateways.backoff_initial_sec must be >= 0, got %d", c.Gateways.BackoffInitialSec))
	}
	if c.Gateways.UpdateEveryMs < 1 {
		errs = append(errs, fmt.Errorf("config: gateways.update_every_ms must be >= 1, got %d", c.Gateways.UpdateEveryMs))
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

	if c.DatabaseURL == "" && os.Getenv("DATABASE_URL") == "" {
		warns = append(warns, Warning("database_url not configured and DATABASE_URL env var not set; all persistent stores (sessions, memory, learning) require Postgres — set with: overkill config set database_url <connection-string>"))
	}

	// Cost limiting is opt-in — no warning when unset (user may not want budget tracking)

	// Sandbox warnings for high-autonomy modes (these are safety-relevant, keep them)
	if c.Security.AutonomyLevel == "auto" && !c.Security.SandboxEnabled {
		warns = append(warns, Warning("auto autonomy without sandbox enabled; auto-mode will execute without human confirmation — sandbox strongly recommended"))
	}
	if (c.Security.AutonomyLevel == "full" || c.Security.AutonomyLevel == "yolo") && !c.Security.SandboxEnabled {
		warns = append(warns, Warning(fmt.Sprintf("%s autonomy without sandbox enabled; consider enabling sandbox for safety", c.Security.AutonomyLevel)))
	}

	return warns
}
