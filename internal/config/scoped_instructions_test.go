package config

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestScopedInstructionsExplicitConfig(t *testing.T) {
	require.False(t, Default().Agent.Instructions.Enabled)
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("[agent.instructions]\nenabled = true\nglobal_file = '/example/global.md'\ndirectory = 'backend'\n"), 0600))
	cfg, err := Load(path)
	require.NoError(t, err)
	require.True(t, cfg.Agent.Instructions.Enabled)
	require.Equal(t, "/example/global.md", cfg.Agent.Instructions.GlobalFile)
	require.Equal(t, "backend", cfg.Agent.Instructions.Directory)
}
