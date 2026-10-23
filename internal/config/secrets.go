package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func (c *Config) MaskSecrets() *Config {
	if c == nil {
		return nil
	}
	cp, err := c.credentialCopy()
	if err != nil {
		return nil
	}
	for _, field := range cp.credentialFields() {
		if field.secret {
			field.set(mask(field.get()))
		}
	}
	return cp
}

var knownKeyPrefixes = []string{
	"sk-ant-api", // Anthropic Claude API
	"sk-ant-",    // Anthropic
	"sk-",        // OpenAI / compatible
	"anthropic-", // Anthropic raw
	"deepseek-",  // DeepSeek
	"xai-",       // xAI / Grok
	"glm-",       // GLM / Z.AI
	"kim-",       // Kimi / Moonshot
	"minimax-",   // MiniMax
}

// MaskKey returns s with the middle redacted, preserving any known
// key prefix plus the last 4 characters. For a typical OpenAI key
// "sk-8c9c6b39a2e7406ca5bf06ac" this yields "sk-8c...06ac".
func MaskKey(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return strings.Repeat("*", len(s))
	}

	// Find the longest matching known prefix.
	prefixLen := 0
	for _, p := range knownKeyPrefixes {
		if strings.HasPrefix(s, p) && len(p) > prefixLen {
			prefixLen = len(p)
		}
	}

	var visiblePrefix string
	if prefixLen > 0 {
		// Show whole prefix + 2 chars of the key body so the user
		// can tell keys apart (e.g. "sk-8c" vs "sk-9a").
		bodyStart := prefixLen
		bodyShow := 2
		if bodyStart+bodyShow > len(s)-4 {
			bodyShow = len(s) - 4 - bodyStart
		}
		if bodyShow < 0 {
			bodyShow = 0
		}
		visiblePrefix = s[:bodyStart+bodyShow]
	} else {
		// No known prefix — show first 4 characters.
		visiblePrefix = s[:4]
	}

	return visiblePrefix + "..." + s[len(s)-4:]
}

func mask(s string) string {
	return MaskKey(s)
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
