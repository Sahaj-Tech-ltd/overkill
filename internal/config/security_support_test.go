package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxDefaultsAndExplicitFalseAreSupported(t *testing.T) {
	require.False(t, Default().Security.SandboxEnabled)
	for _, toml := range []string{"version = 1\n", "version = 1\n[security]\nsandbox_enabled = false\n"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(path, []byte(toml), 0600))
		cfg, err := Load(path)
		require.NoError(t, err)
		require.False(t, cfg.Security.SandboxEnabled)
		require.NoError(t, cfg.Security.ValidateSupported())
		require.Empty(t, cfg.Validate())
	}
}

func TestSandboxExplicitTrueRejectedByLoadWithoutRewriting(t *testing.T) {
	for _, version := range []string{"0", "1"} {
		t.Run(version, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			input := []byte("version = " + version + "\n[security]\nsandbox_enabled = true\n")
			require.NoError(t, os.WriteFile(path, input, 0600))
			cfg, err := Load(path)
			require.Nil(t, cfg)
			require.ErrorIs(t, err, ErrSandboxUnsupported)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, input, after)
		})
	}
}

func TestSandboxSaveAndValidateRejectWithoutClearingRequest(t *testing.T) {
	cfg := Default()
	cfg.Security.SandboxEnabled = true
	require.ErrorIs(t, cfg.Security.ValidateSupported(), ErrSandboxUnsupported)
	var found bool
	for _, err := range cfg.Validate() {
		found = found || errors.Is(err, ErrSandboxUnsupported)
	}
	require.True(t, found)
	path := filepath.Join(t.TempDir(), "not-created", "config.toml")
	require.ErrorIs(t, cfg.Save(path), ErrSandboxUnsupported)
	_, err := os.Stat(filepath.Dir(path))
	require.True(t, os.IsNotExist(err))
	require.True(t, cfg.Security.SandboxEnabled)
}

func TestSandboxDoctorPreservesExplicitUnsupportedRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OVERKILL_HOME", home)
	path := filepath.Join(home, "config.toml")
	input := []byte("version = 0\n[security]\nsandbox_enabled = true\n")
	require.NoError(t, os.WriteFile(path, input, 0600))
	_, err := Doctor()
	require.ErrorIs(t, err, ErrSandboxUnsupported)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, input, after)
}

func TestSandboxWarningsDescribeHostExecution(t *testing.T) {
	for _, level := range []string{"auto", "full", "yolo"} {
		cfg := Default()
		cfg.Security.AutonomyLevel = level
		var found bool
		for _, warning := range cfg.Warnings() {
			text := warning.String()
			require.NotContains(t, text, "consider enabling sandbox")
			require.NotContains(t, text, "sandbox strongly recommended")
			found = found || strings.Contains(text, "execution sandbox unavailable")
		}
		require.True(t, found, level)
	}
}
