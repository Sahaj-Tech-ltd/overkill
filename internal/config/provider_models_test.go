package config

import (
	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModelConfigSeparatesLegacyContextFromOutput(t *testing.T) {
	var entry ModelConfig
	err := toml.Unmarshal([]byte("id='custom'\nmax_tokens=64000\ncontext_window=128000\nmax_output_tokens=4096\ncost_in=2\n"), &entry)
	require.NoError(t, err)
	mapped := ProviderModels([]ModelConfig{entry})[0]
	require.Equal(t, 128000, mapped.ContextWindow)
	require.Equal(t, 64000, mapped.MaxTokens)
	require.Equal(t, 4096, mapped.DefaultMaxTokens)
	require.Equal(t, float64(2), mapped.CostIn)
	mapped = ProviderModels([]ModelConfig{{ID: "legacy", MaxTokens: 200000}})[0]
	require.Equal(t, 200000, mapped.ContextWindow)
	require.Zero(t, mapped.DefaultMaxTokens)
}

func TestConfigRejectsInvalidModelTokenOverrides(t *testing.T) {
	for _, model := range []ModelConfig{{ID: "bad", ContextWindow: -1}, {ID: "bad", MaxTokens: -1}, {ID: "bad", MaxOutputTokens: -1}, {ID: "bad", ContextWindow: 1000, MaxOutputTokens: 1000}} {
		cfg := Default()
		cfg.Providers = []ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", Models: []ModelConfig{model}}}
		require.NotEmpty(t, cfg.Validate())
	}
}
