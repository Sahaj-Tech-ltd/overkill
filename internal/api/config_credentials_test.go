package api

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

type onboardingCredentialStore struct {
	values map[string]string
	fail   bool
	writes int
}

func (s *onboardingCredentialStore) Set(account, value string) error {
	if s.fail {
		return errors.New("fixture-key-must-not-appear-in-RPC-error")
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[account] = value
	s.writes++
	return nil
}
func (s *onboardingCredentialStore) Get(account string) (string, error) {
	v, ok := s.values[account]
	if !ok {
		return "", errors.New("missing")
	}
	return v, nil
}
func (s *onboardingCredentialStore) Delete(account string) error {
	delete(s.values, account)
	return nil
}

func TestConfigCreateHTTPUsesSelectedProfileVaultAndThemeRestart(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("OVERKILL_HOME", t.TempDir())
	selected := filepath.Join(t.TempDir(), "chosen.toml")
	initial := config.Default()
	require.NoError(t, initial.Save(selected))
	loaded, err := config.Load(selected)
	require.NoError(t, err)
	store := &onboardingCredentialStore{}
	// Exercise public construction to prove the explicit injection is wired.
	constructed := NewServer(ServerConfig{Config: loaded, ConfigCredentialStore: store})
	require.Same(t, store, constructed.configCredentialStore)
	s, httpServer := newTestServer(t)
	s.cfg = loaded
	s.configCredentialStore = store
	params := map[string]any{
		"providers": []map[string]any{{"name": "fixture", "type": "openai", "apiKey": "fixture-onboarding-provider-key", "headers": map[string]string{"Authorization": "Bearer fixture-onboarding-header"}, "models": []map[string]string{{"id": "fixture-model"}}}},
		"tts":       map[string]string{"provider": "openai", "apiKey": "fixture-onboarding-tts-key", "voice": "fixture"},
	}
	response := readRPCResponse(t, doRPC(t, httpServer, "config.create", params))
	require.Nil(t, response.Error)
	require.Equal(t, "fixture-onboarding-provider-key", s.cfg.Providers[0].APIKey)
	require.Equal(t, "fixture-onboarding-tts-key", s.cfg.TTS.OpenAIKey)
	require.Equal(t, 3, store.writes)
	raw, err := os.ReadFile(selected)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "fixture-onboarding-")
	require.Contains(t, string(raw), "keyring://")
	response = readRPCResponse(t, doRPC(t, httpServer, "config.theme", map[string]string{"theme": "ocean"}))
	require.Nil(t, response.Error)
	restart, err := config.Load(selected)
	require.NoError(t, err)
	require.NoError(t, restart.ResolveSecretsWithStore(store))
	require.Equal(t, "fixture-onboarding-provider-key", restart.Providers[0].APIKey)
	require.Equal(t, "fixture-onboarding-tts-key", restart.TTS.OpenAIKey)
	require.Equal(t, "ocean", restart.UI.Theme)
	raw, err = os.ReadFile(selected)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "fixture-onboarding-")
	defaultPath, err := config.ConfigPath()
	require.NoError(t, err)
	_, err = os.Stat(defaultPath)
	require.True(t, os.IsNotExist(err), "selected profile must not spill into default profile")
}

func TestConfigCreateHTTPVaultFailureKeepsDiskAndRuntime(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	selected := filepath.Join(t.TempDir(), "chosen.toml")
	original := config.Default()
	original.UI.Theme = "ocean"
	require.NoError(t, original.Save(selected))
	loaded, err := config.Load(selected)
	require.NoError(t, err)
	old, err := os.ReadFile(selected)
	require.NoError(t, err)
	s, httpServer := newTestServer(t)
	s.cfg = loaded
	s.configCredentialStore = &onboardingCredentialStore{fail: true}
	response := readRPCResponse(t, doRPC(t, httpServer, "config.create", map[string]any{"providers": []map[string]string{{"name": "fixture", "apiKey": "fixture-key-must-not-appear-in-RPC-error"}}}))
	require.NotNil(t, response.Error)
	require.Equal(t, InternalError, response.Error.Code)
	require.NotContains(t, response.Error.Message, "fixture-key")
	require.Same(t, loaded, s.cfg)
	require.Equal(t, "ocean", s.cfg.UI.Theme)
	actual, err := os.ReadFile(selected)
	require.NoError(t, err)
	require.Equal(t, old, actual)
}
