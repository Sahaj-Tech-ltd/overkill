package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNegativeMCPByteLimitRejectedByLoadSaveAndValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	source := []byte("version = 1\n[[mcp.servers]]\nname = 'fixture'\ncommand = 'must-not-launch'\nmax_bytes_per_call = -1\n")
	require.NoError(t, os.WriteFile(path, source, 0600))
	_, err := Load(path)
	require.ErrorContains(t, err, "max_bytes_per_call")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, source, after)
	cfg := Default()
	cfg.MCP.Servers = []MCPServer{{Name: "fixture", Command: "must-not-launch", MaxBytesPerCall: -1}}
	errors := cfg.Validate()
	found := false
	for _, err := range errors {
		if strings.Contains(err.Error(), "max_bytes_per_call") {
			found = true
		}
	}
	require.True(t, found)
	target := filepath.Join(t.TempDir(), "uncreated", "config.toml")
	require.ErrorContains(t, cfg.Save(target), "max_bytes_per_call")
	_, err = os.Stat(filepath.Dir(target))
	require.True(t, os.IsNotExist(err))
}
