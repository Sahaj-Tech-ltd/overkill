package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeCredentialStore struct {
	values                          map[string]string
	setCalls, getCalls, deleteCalls int
	failSetAt                       int
	failGet                         bool
}

func (s *fakeCredentialStore) Set(account, value string) error {
	s.setCalls++
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[account] = value
	if s.failSetAt == s.setCalls {
		return errors.New("simulated vault denial without disclosure")
	}
	return nil
}
func (s *fakeCredentialStore) Get(account string) (string, error) {
	s.getCalls++
	if s.failGet {
		return "", errors.New("vault denied")
	}
	v, ok := s.values[account]
	if !ok {
		return "", errors.New("missing")
	}
	return v, nil
}
func (s *fakeCredentialStore) Delete(account string) error {
	s.deleteCalls++
	delete(s.values, account)
	return nil
}

func TestCredentialSecureSaveRestartAndOrdinarySave(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	selected := filepath.Join(t.TempDir(), "chosen.toml")
	store := &fakeCredentialStore{}
	cfg := Default()
	cfg.Providers = []ProviderConfig{{Name: "provider", APIKey: "fixture-provider-secret", BaseURL: "https://api.example.test", Headers: map[string]string{"Authorization": "Bearer fixture-header-secret"}}}
	cfg.TTS.OpenAIKey = "fixture-tts-secret"
	cfg.Gateways.Mattermost.BotToken = "fixture-mattermost-secret"
	cfg.MCP.Servers = []MCPServer{{Name: "fixture", Env: map[string]string{"TOKEN": "fixture-mcp-secret"}}}
	require.NoError(t, cfg.SaveWithCredentials(selected, store))
	require.Equal(t, "fixture-provider-secret", cfg.Providers[0].APIKey)
	raw, err := os.ReadFile(selected)
	require.NoError(t, err)
	for _, secret := range []string{"fixture-provider-secret", "fixture-header-secret", "fixture-tts-secret", "fixture-mattermost-secret", "fixture-mcp-secret"} {
		require.NotContains(t, string(raw), secret)
	}
	require.Equal(t, 5, store.setCalls)
	require.Equal(t, 5, len(store.values))
	require.Contains(t, string(raw), keyringPrefix)
	loaded, err := Load(selected)
	require.NoError(t, err)
	require.NoError(t, loaded.ResolveSecretsWithStore(store))
	require.Equal(t, cfg.Providers[0].APIKey, loaded.Providers[0].APIKey)
	require.Equal(t, cfg.MCP.Servers[0].Env, loaded.MCP.Servers[0].Env)
	loaded.UI.Theme = "ocean"
	require.NoError(t, loaded.Save(selected))
	raw, err = os.ReadFile(selected)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "fixture-provider-secret")
	require.NotContains(t, string(raw), "fixture-mcp-secret")
	restarted, err := Load(selected)
	require.NoError(t, err)
	require.NoError(t, restarted.ResolveSecretsWithStore(store))
	require.Equal(t, "ocean", restarted.UI.Theme)
	require.Equal(t, "fixture-tts-secret", restarted.TTS.OpenAIKey)
	require.Equal(t, 5, store.setCalls, "unrelated saves must not rotate credentials")
}

func TestCredentialEveryInventoriedSecretIsVaulted(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	cfg := Default()
	cfg.Providers = []ProviderConfig{{Name: "fixture", Headers: map[string]string{"Authorization": ""}}}
	cfg.MCP.Servers = []MCPServer{{Name: "fixture", Env: map[string]string{"TOKEN": ""}}}
	expected := 0
	for _, field := range cfg.credentialFields() {
		if field.secret {
			field.set("private-value-for-" + field.name)
			expected++
		}
	}
	selected := filepath.Join(t.TempDir(), "all-fields.toml")
	store := &fakeCredentialStore{}
	require.NoError(t, cfg.SaveWithCredentials(selected, store))
	raw, err := os.ReadFile(selected)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-value-for-")
	require.Equal(t, expected, len(store.values))
	restarted, err := Load(selected)
	require.NoError(t, err)
	require.NoError(t, restarted.ResolveSecretsWithStore(store))
	for _, field := range restarted.credentialFields() {
		if field.secret {
			require.Equal(t, "private-value-for-"+field.name, field.get(), field.name)
		}
	}
}

func TestCredentialEnvironmentOnlyNeverAccessesVaultOrLeaks(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://fixture:env-database-password@127.0.0.1/test")
	t.Setenv("OVERKILL_TEST_PROVIDER_SECRET", "fixture-env-provider-secret")
	cfg := Default()
	cfg.Providers = []ProviderConfig{{Name: "fixture", APIKey: "${OVERKILL_TEST_PROVIDER_SECRET}", Headers: map[string]string{"Authorization": "Bearer ${OVERKILL_TEST_PROVIDER_SECRET}"}}}
	store := &fakeCredentialStore{}
	require.NoError(t, cfg.ResolveSecretsWithStore(store))
	path := filepath.Join(t.TempDir(), "env.toml")
	require.NoError(t, cfg.SaveWithCredentials(path, store))
	require.Zero(t, store.setCalls)
	require.Zero(t, store.getCalls)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "fixture-env-provider-secret")
	require.NotContains(t, string(raw), "env-database-password")
	require.Contains(t, string(raw), "${OVERKILL_TEST_PROVIDER_SECRET}")
	require.Contains(t, string(raw), "${DATABASE_URL}")
}

func TestCredentialFailureKeepsOldProfileAndMemory(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, failure := range []string{"set", "read", "publish"} {
		t.Run(failure, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "profile")
			require.NoError(t, os.Mkdir(dir, 0700))
			path := filepath.Join(dir, "config.toml")
			cfg := Default()
			cfg.Providers = []ProviderConfig{{Name: "fixture", APIKey: "fixture-old-secret"}}
			store := &fakeCredentialStore{}
			require.NoError(t, cfg.SaveWithCredentials(path, store))
			old, err := os.ReadFile(path)
			require.NoError(t, err)
			oldAccounts := map[string]string{}
			for key, value := range store.values {
				oldAccounts[key] = value
			}
			cfg.Providers[0].APIKey = "fixture-new-secret"
			if failure == "set" {
				store.failSetAt = store.setCalls + 1
			}
			if failure == "read" {
				store.failGet = true
			}
			if failure == "publish" {
				require.NoError(t, os.Rename(dir, dir+"-old"))
				require.NoError(t, os.WriteFile(dir, []byte("unchanged sentinel"), 0600))
			}
			require.Error(t, cfg.SaveWithCredentials(path, store))
			require.Equal(t, "fixture-new-secret", cfg.Providers[0].APIKey, "failure must not replace caller's pre-operation memory")
			require.Equal(t, oldAccounts, store.values, "delete only newly staged accounts")
			if failure == "publish" {
				path = filepath.Join(dir+"-old", "config.toml")
			}
			actual, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, old, actual)
		})
	}
}

func TestCredentialProfilesRotationAndProviderReordering(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	store := &fakeCredentialStore{}
	first, second := Default(), Default()
	first.Providers = []ProviderConfig{{Name: "one", APIKey: "fixture-shared-secret"}, {Name: "two", APIKey: "fixture-second-secret"}}
	second.Providers = []ProviderConfig{{Name: "one", APIKey: "fixture-shared-secret"}}
	path1, path2 := filepath.Join(t.TempDir(), "one.toml"), filepath.Join(t.TempDir(), "two.toml")
	require.NoError(t, first.SaveWithCredentials(path1, store))
	require.NoError(t, second.SaveWithCredentials(path2, store))
	require.Len(t, store.values, 3)
	b1, b2 := first.secretBindings["providers.0.api_key"], second.secretBindings["providers.0.api_key"]
	require.NotEqual(t, b1.Reference, b2.Reference)
	require.NotEqual(t, strings.Split(b1.Reference, "/")[2], strings.Split(b2.Reference, "/")[2])
	first.Providers[0], first.Providers[1] = first.Providers[1], first.Providers[0]
	require.NoError(t, first.Save(path1))
	raw, err := os.ReadFile(path1)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "fixture-")
	restart, err := Load(path1)
	require.NoError(t, err)
	require.NoError(t, restart.ResolveSecretsWithStore(store))
	require.Equal(t, "fixture-second-secret", restart.Providers[0].APIKey)
	first.Providers[0].APIKey = "fixture-rotated-secret"
	require.NoError(t, first.SaveWithCredentials(path1, store))
	require.Len(t, store.values, 4, "old credentials remain valid for prior snapshots")
	require.Equal(t, "fixture-shared-secret", store.values[strings.TrimPrefix(b1.Reference, keyringPrefix)])
}

func TestCredentialResolveFailureAtomicAndMaskDoesNotMutate(t *testing.T) {
	t.Setenv("OVERKILL_TEST_PROVIDER_SECRET", "fixture-env-secret")
	cfg := Default()
	cfg.Providers = []ProviderConfig{{Name: "fixture", APIKey: "${OVERKILL_TEST_PROVIDER_SECRET}", Headers: map[string]string{"Authorization": "Bearer fixture-private-header"}}}
	cfg.Gateways.Matrix.Password = "${OVERKILL_NONEXISTENT_FIXTURE}"
	before := cfg.Providers[0].APIKey
	require.Error(t, cfg.ResolveSecretsWithStore(&fakeCredentialStore{}))
	require.Equal(t, before, cfg.Providers[0].APIKey)
	masked := cfg.MaskSecrets()
	require.NotNil(t, masked)
	require.NotEqual(t, masked.Providers[0].Headers, cfg.Providers[0].Headers)
	require.Equal(t, "Bearer fixture-private-header", cfg.Providers[0].Headers["Authorization"])
}

func TestCredentialSaveAsCreatesIndependentProfileEntries(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	store := &fakeCredentialStore{}
	cfg := Default()
	cfg.Providers = []ProviderConfig{{Name: "fixture", APIKey: "fixture-save-as-secret"}}
	first, second := filepath.Join(t.TempDir(), "first.toml"), filepath.Join(t.TempDir(), "second.toml")
	require.NoError(t, cfg.SaveWithCredentials(first, store))
	prior := cfg.secretBindings["providers.0.api_key"].Reference
	require.NoError(t, cfg.SaveWithCredentials(second, store))
	require.NotEqual(t, prior, cfg.secretBindings["providers.0.api_key"].Reference)
	require.Len(t, store.values, 2)
	for _, path := range []string{first, second} {
		loaded, err := Load(path)
		require.NoError(t, err)
		require.NoError(t, loaded.ResolveSecretsWithStore(store))
		require.Equal(t, "fixture-save-as-secret", loaded.Providers[0].APIKey)
	}
}
