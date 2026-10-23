package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadRejectsMalformedSettingsWithoutChangingFile(t *testing.T) {
	for name, contents := range map[string]string{
		"security syntax":         "[security]\ndeny_patterns = [\"rm -rf\"\n",
		"security wrong type":     "[security]\ndeny_patterns = true\n",
		"provider obsolete table": "[providers.openai]\napi_key = \"secret-placeholder\"\n",
		"provider wrong type":     "[[providers]]\nname = [\"openai\"]\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			require.NoError(t, os.WriteFile(path, []byte(contents), 0600))
			cfg, err := Load(path)
			require.ErrorContains(t, err, "failed to parse config file")
			require.ErrorContains(t, err, path)
			require.Nil(t, cfg, "invalid security or provider settings must never become usable defaults")
			after, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			require.Equal(t, contents, string(after))
		})
	}
}

func TestLoadDefaultPathUsesExistingConfiguration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OVERKILL_HOME", dir)
	path := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("[security]\nforbidden_paths = false\n"), 0600))
	cfg, err := Load("")
	require.ErrorContains(t, err, path)
	require.Nil(t, cfg)
}

func TestLoadMissingConfigBootstrapsAndReportsWriteFailure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "config.toml")
		cfg, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, CurrentVersion, cfg.Version)
		loaded, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, cfg.Version, loaded.Version)
		require.Equal(t, cfg.Agent, loaded.Agent)
		require.Equal(t, cfg.Security, loaded.Security)
	})
	t.Run("invalid parent", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "regular-file")
		require.NoError(t, os.WriteFile(parent, nil, 0600))
		cfg, err := Load(filepath.Join(parent, "config.toml"))
		require.Error(t, err)
		require.Nil(t, cfg)
	})
}
