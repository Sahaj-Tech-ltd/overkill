package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestInitializeDefaultConfigUsesSchemaWithoutResolvingProviderSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "secret-must-not-be-written")
	t.Setenv("ANTHROPIC_API_KEY", "another-secret-must-not-be-written")
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, initializeDefaultConfig(path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-must-not-be-written")
	loaded, err := config.Load(path)
	require.NoError(t, err)
	require.Empty(t, loaded.Validate())
	require.Empty(t, loaded.Providers)
}

func TestInitializeDefaultConfigPreservesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("existing user configuration")
	require.NoError(t, os.WriteFile(path, original, 0o600))
	require.Error(t, initializeDefaultConfig(path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, data)
	temporary, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".overkill-config-*"))
	require.NoError(t, err)
	require.Empty(t, temporary)
}
