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
	cp := *c
	cp.Providers = make([]ProviderConfig, len(c.Providers))
	for i, p := range c.Providers {
		pp := p
		pp.APIKey = mask(p.APIKey)
		cp.Providers[i] = pp
	}
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
