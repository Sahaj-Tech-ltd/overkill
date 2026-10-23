package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

func TestConfigUpdateRejectsUnsupportedSandboxBeforeAnyMutation(t *testing.T) {
	for _, value := range []any{true, "true", 1, nil} {
		t.Run(stringValue(value), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OVERKILL_HOME", home)
			cfg := config.Default()
			path := filepath.Join(home, "config.toml")
			require.NoError(t, cfg.Save(path))
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			s := NewServer(ServerConfig{Config: cfg})
			params, err := json.Marshal(map[string]any{"patch": map[string]any{"agent": map[string]any{"default_model": "should-not-change"}, "ui": map[string]any{"animations": false}, "security": map[string]any{"sandbox_enabled": value}}})
			require.NoError(t, err)
			result, rpcErr := s.handleConfigUpdate(context.Background(), params)
			require.Nil(t, result)
			require.NotNil(t, rpcErr)
			require.Equal(t, InvalidParams, rpcErr.Code)
			require.Contains(t, rpcErr.Message, "sandbox_enabled")
			require.Equal(t, "", cfg.Agent.DefaultModel)
			require.True(t, cfg.UI.Animations)
			require.False(t, cfg.Security.SandboxEnabled)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func stringValue(value any) string { b, _ := json.Marshal(value); return string(b) }

func TestConfigSandboxReportsUnavailableAndAcceptsExplicitFalse(t *testing.T) {
	t.Setenv("OVERKILL_HOME", t.TempDir())
	cfg := config.Default()
	s := NewServer(ServerConfig{Config: cfg})
	response, rpcErr := s.handleConfigGet(context.Background(), nil)
	require.Nil(t, rpcErr)
	security := response.(*ConfigGetResult).Security
	require.Equal(t, false, security["sandbox_available"])
	require.Equal(t, "unavailable", security["sandbox_status"])
	require.Contains(t, security["sandbox_reason"], "not isolated")
	cfg.Security.SandboxEnabled = true // Programmatic invalid state must require explicit correction.
	_, rpcErr = s.handleConfigUpdate(context.Background(), []byte(`{"patch":{"ui":{"animations":false}}}`))
	require.NotNil(t, rpcErr)
	require.True(t, cfg.Security.SandboxEnabled)
	require.True(t, cfg.UI.Animations)
	_, rpcErr = s.handleConfigUpdate(context.Background(), []byte(`{"patch":{"security":{"sandbox_enabled":false}}}`))
	require.Nil(t, rpcErr)
	require.False(t, cfg.Security.SandboxEnabled)
	path, err := config.ConfigPath()
	require.NoError(t, err)
	saved, err := config.Load(path)
	require.NoError(t, err)
	require.False(t, saved.Security.SandboxEnabled)
}

func TestAPIRuntimeRejectsUnsupportedSandboxBeforeBuildingTools(t *testing.T) {
	cfg := config.Default()
	cfg.Security.SandboxEnabled = true
	called := false
	s := NewServer(ServerConfig{Config: cfg, SessionRuntimeFactory: func(context.Context, *session.Session) (*SessionRuntime, error) {
		called = true
		return &SessionRuntime{}, nil
	}})
	a, err := s.createAgent(context.Background(), "not-built")
	require.Nil(t, a)
	require.ErrorIs(t, err, config.ErrSandboxUnsupported)
	require.False(t, called)
	require.Empty(t, s.agents)
	require.Empty(t, s.sessionRuntimes)
}

func TestSandboxConfigRPCRouteRejectsEnable(t *testing.T) {
	t.Setenv("OVERKILL_HOME", t.TempDir())
	s, server := newTestServer(t)
	response := doRPC(t, server, "config.update", map[string]any{"patch": map[string]any{"security": map[string]any{"sandbox_enabled": true}}})
	rpc := readRPCResponse(t, response)
	require.NotNil(t, rpc.Error)
	require.Equal(t, InvalidParams, rpc.Error.Code)
	require.Contains(t, rpc.Error.Message, "unsupported")
	require.False(t, s.cfg.Security.SandboxEnabled)
}
