package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func (c *Config) ResolveSecrets() error {
	for i := range c.Providers {
		resolved, err := resolveEnvRefs(c.Providers[i].APIKey)
		if err != nil {
			return fmt.Errorf("config: resolving api_key for provider %q: %w", c.Providers[i].Name, err)
		}
		c.Providers[i].APIKey = resolved

		resolved, err = resolveEnvRefs(c.Providers[i].BaseURL)
		if err != nil {
			return fmt.Errorf("config: resolving base_url for provider %q: %w", c.Providers[i].Name, err)
		}
		c.Providers[i].BaseURL = resolved

		for key, val := range c.Providers[i].Headers {
			resolved, err := resolveEnvRefs(val)
			if err != nil {
				return fmt.Errorf("config: resolving header %q for provider %q: %w", key, c.Providers[i].Name, err)
			}
			c.Providers[i].Headers[key] = resolved
		}
	}

	return nil
}

func (c *Config) MaskSecrets() *Config {
	if c == nil {
		return nil
	}
	cp := *c
	cp.Providers = make([]ProviderConfig, len(c.Providers))
	for i, p := range c.Providers {
		pp := p
		pp.APIKey = mask(p.APIKey)
		for k, v := range pp.Headers {
			pp.Headers[k] = mask(v)
		}
		cp.Providers[i] = pp
	}
	// TTSConfig secrets
	cp.TTS.OpenAIKey = mask(c.TTS.OpenAIKey)
	cp.TTS.ElevenLabsKey = mask(c.TTS.ElevenLabsKey)
	// ImageGenConfig secrets
	cp.ImageGen.OpenAIKey = mask(c.ImageGen.OpenAIKey)
	cp.ImageGen.StabilityKey = mask(c.ImageGen.StabilityKey)
	cp.ImageGen.ReplicateToken = mask(c.ImageGen.ReplicateToken)
	// SlackConfig secrets (both top-level and inside gateways)
	cp.Slack.AppToken = mask(c.Slack.AppToken)
	cp.Slack.BotToken = mask(c.Slack.BotToken)
	cp.Gateways.Slack.AppToken = mask(c.Gateways.Slack.AppToken)
	cp.Gateways.Slack.BotToken = mask(c.Gateways.Slack.BotToken)
	// Gateway secrets
	cp.Gateways.Telegram.BotToken = mask(c.Gateways.Telegram.BotToken)
	cp.Gateways.Discord.BotToken = mask(c.Gateways.Discord.BotToken)
	cp.Gateways.Bridge.Token = mask(c.Gateways.Bridge.Token)
	cp.Gateways.Signal.AuthToken = mask(c.Gateways.Signal.AuthToken)
	cp.Gateways.Matrix.AccessToken = mask(c.Gateways.Matrix.AccessToken)
	cp.Gateways.Matrix.Password = mask(c.Gateways.Matrix.Password)
	cp.Gateways.WhatsApp.Cloud.AccessToken = mask(c.Gateways.WhatsApp.Cloud.AccessToken)
	cp.Gateways.WhatsApp.Cloud.AppSecret = mask(c.Gateways.WhatsApp.Cloud.AppSecret)
	cp.Gateways.WhatsApp.Cloud.VerifyToken = mask(c.Gateways.WhatsApp.Cloud.VerifyToken)
	// SyncS3Config secrets
	cp.Sync.S3.AccessKey = mask(c.Sync.S3.AccessKey)
	cp.Sync.S3.SecretKey = mask(c.Sync.S3.SecretKey)
	// ShareConfig secrets
	cp.Share.GitHubToken = mask(c.Share.GitHubToken)
	// ACPConfig secrets
	cp.ACP.Token = mask(c.ACP.Token)
	// OuroborosConfig secrets
	cp.Ouroboros.APIKey = mask(c.Ouroboros.APIKey)
	// VisionConfig secrets
	cp.Vision.APIKey = mask(c.Vision.APIKey)
	// DatabaseURL may contain password in connection string
	cp.DatabaseURL = mask(c.DatabaseURL)
	return &cp
}

func mask(s string) string {
	if len(s) <= 4 {
		return strings.Repeat("*", len(s))
	}
	return s[:2] + strings.Repeat("*", len(s)-4) + s[len(s)-2:]
}

func resolveEnvRefs(s string) (string, error) {
	if s == "" {
		return "", nil
	}

	var resolveErr error
	result := envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := envVarPattern.FindStringSubmatch(match)[1]
		val, ok := os.LookupEnv(varName)
		if !ok {
			resolveErr = fmt.Errorf("environment variable %q not set", varName)
			return match
		}
		return val
	})

	return result, resolveErr
}
