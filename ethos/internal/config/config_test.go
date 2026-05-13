package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	assert.Equal(t, CurrentVersion, cfg.Version)
	assert.Equal(t, "Overkill", cfg.Agent.Name)
	assert.Equal(t, "openai", cfg.Agent.DefaultProvider)
	assert.Equal(t, "gpt-4o", cfg.Agent.DefaultModel)
	assert.Equal(t, 0, cfg.Agent.MaxTurns)
	assert.False(t, cfg.Agent.SpecDriven)

	assert.Empty(t, cfg.Providers)

	assert.Equal(t, "subtle", cfg.Personality.Level)
	assert.Equal(t, "en", cfg.Personality.Language)

	assert.Equal(t, "supervised", cfg.Security.AutonomyLevel)
	assert.Equal(t, 4096, cfg.Security.MaxCommandLen)
	assert.False(t, cfg.Security.SandboxEnabled)

	assert.True(t, cfg.Session.AutoTitle)
	assert.Equal(t, 0, cfg.Session.MaxSessions)

	assert.Equal(t, 0.0, cfg.Cost.DailyLimitUSD)
	assert.Equal(t, 5, cfg.Cost.RollingWindowHrs)
	assert.Equal(t, 80, cfg.Cost.WarnAtPercent)

	assert.Equal(t, 50, cfg.Compaction.SoftTriggerPercent)
	assert.Equal(t, 95, cfg.Compaction.HardTriggerPercent)
	assert.Equal(t, 20, cfg.Compaction.PreserveMessages)
	assert.Equal(t, 2048, cfg.Compaction.MaxSummaryTokens)
}

func TestDefault_HasDataDir(t *testing.T) {
	cfg := Default()
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)
	expected := filepath.Join(homeDir, ".overkill", "data")
	assert.Equal(t, expected, cfg.Session.DataDir)
}

func TestLoad_NonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")

	cfg, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, CurrentVersion, cfg.Version)
	assert.Equal(t, "Overkill", cfg.Agent.Name)

	_, statErr := os.Stat(path)
	assert.NoError(t, statErr, "default config file should have been created")
}

func TestLoad_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")

	original := Default()
	original.Agent.Name = "TestBot"
	original.Providers = []ProviderConfig{
		{
			Name:   "openai",
			Type:   "openai",
			APIKey: "sk-test-key",
			Models: []ModelConfig{
				{ID: "gpt-4o", Name: "GPT-4o", MaxTokens: 128000},
			},
		},
	}
	require.NoError(t, original.Save(path))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "TestBot", cfg.Agent.Name)
	require.Len(t, cfg.Providers, 1)
	assert.Equal(t, "openai", cfg.Providers[0].Name)
	assert.Equal(t, "sk-test-key", cfg.Providers[0].APIKey)
	require.Len(t, cfg.Providers[0].Models, 1)
	assert.Equal(t, "gpt-4o", cfg.Providers[0].Models[0].ID)
	assert.Equal(t, 128000, cfg.Providers[0].Models[0].MaxTokens)
}

func TestLoad_InvalidTOML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.toml")

	require.NoError(t, os.WriteFile(path, []byte("not valid [[[toml"), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, CurrentVersion, cfg.Version, "should return defaults on parse error")
}

func TestSave_CreateDir(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "nested", "dir", "config.toml")

	cfg := Default()
	err := cfg.Save(path)
	require.NoError(t, err)

	_, statErr := os.Stat(path)
	assert.NoError(t, statErr)
}

func TestConfigDir(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	dir, err := ConfigDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(homeDir, ".overkill"), dir)
}

func TestConfigPath(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	path, err := ConfigPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(homeDir, ".overkill", "config.toml"), path)
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{Name: "openai", Type: "openai", APIKey: "sk-test"},
	}
	errs := cfg.Validate()
	assert.Empty(t, errs)
}

func TestValidate_InvalidAutonomyLevel(t *testing.T) {
	cfg := Default()
	cfg.Security.AutonomyLevel = "dangerous"
	errs := cfg.Validate()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "autonomy_level")
}

func TestValidate_InvalidPersonalityLevel(t *testing.T) {
	cfg := Default()
	cfg.Personality.Level = "chaotic"
	errs := cfg.Validate()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "personality.level")
}

func TestValidate_MissingDefaultProvider(t *testing.T) {
	cfg := Default()
	cfg.Agent.DefaultProvider = "nonexistent"
	cfg.Providers = []ProviderConfig{
		{Name: "openai", Type: "openai", APIKey: "sk-test"},
	}
	errs := cfg.Validate()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "default_provider")
}

func TestValidate_MissingAPIKey(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{Name: "openai", Type: "openai", APIKey: ""},
	}
	errs := cfg.Validate()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "api_key")
}

func TestValidate_OllamaNoKey(t *testing.T) {
	cfg := Default()
	cfg.Agent.DefaultProvider = "local"
	cfg.Providers = []ProviderConfig{
		{Name: "local", Type: "ollama", APIKey: ""},
	}
	errs := cfg.Validate()
	assert.Empty(t, errs, "ollama provider should not require an API key")
}

func TestValidate_NegativeCost(t *testing.T) {
	cfg := Default()
	cfg.Cost.DailyLimitUSD = -5.0
	errs := cfg.Validate()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "daily_limit_usd")
}

func TestValidate_InvalidProviderType(t *testing.T) {
	cfg := Default()
	cfg.Agent.DefaultProvider = "bad"
	cfg.Providers = []ProviderConfig{
		{Name: "bad", Type: "unknown_provider", APIKey: "key"},
	}
	errs := cfg.Validate()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "not a valid provider type")
}

func TestValidate_MissingProviderName(t *testing.T) {
	cfg := Default()
	cfg.Agent.DefaultProvider = ""
	cfg.Providers = []ProviderConfig{
		{Type: "openai", APIKey: "key"},
	}
	errs := cfg.Validate()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "name is required")
}

func TestValidate_CompactionPercent(t *testing.T) {
	cfg := Default()
	cfg.Compaction.SoftTriggerPercent = 150
	errs := cfg.Validate()
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "soft_trigger_percent")
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := Default()
	cfg.Security.AutonomyLevel = "bad"
	cfg.Personality.Level = "bad"
	cfg.Cost.DailyLimitUSD = -1
	errs := cfg.Validate()
	assert.Len(t, errs, 3)
}

func TestWarnings_NoProviders(t *testing.T) {
	cfg := Default()
	warns := cfg.Warnings()
	found := false
	for _, w := range warns {
		if w.String() == "no providers configured; agent will not be able to make LLM calls" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestWarnings_NoDailyLimit(t *testing.T) {
	cfg := Default()
	warns := cfg.Warnings()
	found := false
	for _, w := range warns {
		if w.String() == "cost.daily_limit_usd is not set; usage costs will not be bounded" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestWarnings_OnlyOllama(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{Name: "local", Type: "ollama"},
	}
	warns := cfg.Warnings()
	found := false
	for _, w := range warns {
		if w.String() == "only ollama provider configured; some capabilities may be limited without a cloud provider" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestWarnings_FullAutonomyNoSandbox(t *testing.T) {
	cfg := Default()
	cfg.Security.AutonomyLevel = "full"
	cfg.Security.SandboxEnabled = false
	cfg.Providers = []ProviderConfig{
		{Name: "openai", Type: "openai", APIKey: "sk-test"},
	}
	cfg.Cost.DailyLimitUSD = 10.0
	warns := cfg.Warnings()
	found := false
	for _, w := range warns {
		if w.String() == "full autonomy without sandbox enabled; consider enabling sandbox for safety" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestMigrate_V0ToV1(t *testing.T) {
	cfg := &Config{Version: 0}
	migrated, changes, err := cfg.Migrate()
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	assert.Equal(t, CurrentVersion, migrated.Version)
	assert.Equal(t, "Overkill", migrated.Agent.Name)
	assert.Equal(t, "openai", migrated.Agent.DefaultProvider)
	assert.Equal(t, "subtle", migrated.Personality.Level)
	assert.Equal(t, "supervised", migrated.Security.AutonomyLevel)
	assert.Equal(t, 50, migrated.Compaction.SoftTriggerPercent)
}

func TestMigrate_AlreadyCurrent(t *testing.T) {
	cfg := Default()
	migrated, changes, err := cfg.Migrate()
	require.NoError(t, err)
	assert.Empty(t, changes)
	assert.Equal(t, CurrentVersion, migrated.Version)
}

func TestResolveSecrets_EnvVar(t *testing.T) {
	t.Setenv("TEST_API_KEY", "sk-resolved-secret-123")

	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:   "openai",
			Type:   "openai",
			APIKey: "${TEST_API_KEY}",
		},
	}

	err := cfg.ResolveSecrets()
	require.NoError(t, err)
	assert.Equal(t, "sk-resolved-secret-123", cfg.Providers[0].APIKey)
}

func TestResolveSecrets_MissingEnvVar(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:   "openai",
			Type:   "openai",
			APIKey: "${NONEXISTENT_VAR_12345}",
		},
	}

	err := cfg.ResolveSecrets()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "NONEXISTENT_VAR_12345")
}

func TestResolveSecrets_PlainValue(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:   "openai",
			Type:   "openai",
			APIKey: "sk-plain-key",
		},
	}

	err := cfg.ResolveSecrets()
	require.NoError(t, err)
	assert.Equal(t, "sk-plain-key", cfg.Providers[0].APIKey)
}

func TestResolveSecrets_EmptyValue(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:   "ollama",
			Type:   "ollama",
			APIKey: "",
		},
	}

	err := cfg.ResolveSecrets()
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Providers[0].APIKey)
}

func TestResolveSecrets_BaseURL(t *testing.T) {
	t.Setenv("TEST_BASE_URL", "https://api.example.com/v1")

	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:    "custom",
			Type:    "custom",
			APIKey:  "key",
			BaseURL: "${TEST_BASE_URL}",
		},
	}

	err := cfg.ResolveSecrets()
	require.NoError(t, err)
	assert.Equal(t, "https://api.example.com/v1", cfg.Providers[0].BaseURL)
}

func TestResolveSecrets_Headers(t *testing.T) {
	t.Setenv("TEST_HEADER_VAL", "secret-header")

	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:   "custom",
			Type:   "custom",
			APIKey: "key",
			Headers: map[string]string{
				"X-Auth": "${TEST_HEADER_VAL}",
			},
		},
	}

	err := cfg.ResolveSecrets()
	require.NoError(t, err)
	assert.Equal(t, "secret-header", cfg.Providers[0].Headers["X-Auth"])
}

func TestMaskSecrets(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:   "openai",
			Type:   "openai",
			APIKey: "sk-1234567890abcdef",
		},
	}

	masked := cfg.MaskSecrets()
	assert.Equal(t, "sk***************ef", masked.Providers[0].APIKey)
	assert.Equal(t, "sk-1234567890abcdef", cfg.Providers[0].APIKey, "original should be unchanged")
}

func TestMaskSecrets_ShortKey(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:   "test",
			Type:   "custom",
			APIKey: "abc",
		},
	}

	masked := cfg.MaskSecrets()
	assert.Equal(t, "***", masked.Providers[0].APIKey)
}

func TestMaskSecrets_EmptyKey(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:   "ollama",
			Type:   "ollama",
			APIKey: "",
		},
	}

	masked := cfg.MaskSecrets()
	assert.Equal(t, "", masked.Providers[0].APIKey)
}

func TestMaskSecrets_DoesNotMutateOriginal(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderConfig{
		{
			Name:   "openai",
			Type:   "openai",
			APIKey: "sk-original-key-12345",
		},
	}

	masked := cfg.MaskSecrets()
	_ = masked
	assert.Equal(t, "sk-original-key-12345", cfg.Providers[0].APIKey)
}

func TestDoctor_CreatesMissingDirs(t *testing.T) {
	fixes, err := Doctor()
	require.NoError(t, err)

	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	configDir := filepath.Join(homeDir, ".overkill")
	dataDir := filepath.Join(homeDir, ".overkill", "data")

	info, statErr := os.Stat(configDir)
	assert.NoError(t, statErr, "config directory should exist")
	if statErr == nil {
		assert.True(t, info.IsDir())
	}

	info, statErr = os.Stat(dataDir)
	assert.NoError(t, statErr, "data directory should exist")
	if statErr == nil {
		assert.True(t, info.IsDir())
	}

	_ = fixes
}
