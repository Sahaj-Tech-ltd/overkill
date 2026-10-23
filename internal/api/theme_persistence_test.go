package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestThemeSavesEveryBuiltinToSelectedProfileAndRestarts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OVERKILL_HOME", home)
	selected := filepath.Join(t.TempDir(), "selected.toml")
	cfg := config.Default()
	cfg.UI.Theme = "dark"
	require.NoError(t, cfg.Save(selected))
	loaded, err := config.Load(selected)
	require.NoError(t, err)
	s := &Server{cfg: loaded}
	for _, name := range config.BuiltinThemes() {
		raw, err := json.Marshal(map[string]string{"theme": name})
		require.NoError(t, err)
		result, rpcErr := s.handleConfigTheme(t.Context(), raw)
		require.Nil(t, rpcErr)
		require.Equal(t, name, result.(map[string]string)["theme"])
		persisted, err := config.Load(selected)
		require.NoError(t, err)
		require.Equal(t, name, persisted.UI.Theme)
	}
	_, err = os.Stat(filepath.Join(home, "config.toml"))
	require.True(t, os.IsNotExist(err), "must not write a different profile")
	loaded, err = config.Load(selected)
	require.NoError(t, err)
	restarted := &Server{cfg: loaded}
	result, rpcErr := restarted.handleConfigTheme(t.Context(), nil)
	require.Nil(t, rpcErr)
	require.Equal(t, s.cfg.UI.Theme, result.(map[string]string)["theme"])
}

func TestThemePersistenceFailureNeverPublishesMemorySuccess(t *testing.T) {
	t.Setenv("OVERKILL_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "profile")
	require.NoError(t, os.Mkdir(dir, 0700))
	selected := filepath.Join(dir, "selected.toml")
	cfg := config.Default()
	cfg.UI.Theme = "dark"
	require.NoError(t, cfg.Save(selected))
	loaded, err := config.Load(selected)
	require.NoError(t, err)
	// Replace the parent with a regular sentinel: deterministic failed publication
	// on every platform, without depending on administrator permission semantics.
	require.NoError(t, os.Rename(dir, dir+"-backup"))
	require.NoError(t, os.WriteFile(dir, []byte("preserve sentinel"), 0600))
	s := &Server{cfg: loaded}
	result, rpcErr := s.handleConfigTheme(t.Context(), json.RawMessage(`{"theme":"light"}`))
	require.Nil(t, result)
	require.NotNil(t, rpcErr)
	require.Equal(t, InternalError, rpcErr.Code)
	require.Equal(t, "dark", loaded.UI.Theme)
	sentinel, err := os.ReadFile(dir)
	require.NoError(t, err)
	require.Equal(t, "preserve sentinel", string(sentinel))
	prior, err := config.Load(filepath.Join(dir+"-backup", "selected.toml"))
	require.NoError(t, err)
	require.Equal(t, "dark", prior.UI.Theme)
}

func TestConcurrentThemePublicationKeepsMemoryAndFileConsistent(t *testing.T) {
	t.Setenv("OVERKILL_HOME", t.TempDir())
	selected := filepath.Join(t.TempDir(), "theme.toml")
	cfg := config.Default()
	require.NoError(t, cfg.Save(selected))
	loaded, err := config.Load(selected)
	require.NoError(t, err)
	s := &Server{cfg: loaded}
	var wg sync.WaitGroup
	for _, name := range []string{"dark", "light", "nord", "cyberpunk", "catppuccin-frappe", "ocean"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]string{"theme": name})
			_, rpcErr := s.handleConfigTheme(t.Context(), raw)
			if rpcErr != nil {
				t.Error(rpcErr)
			}
		}()
	}
	wg.Wait()
	persisted, err := config.Load(selected)
	require.NoError(t, err)
	result, rpcErr := s.handleConfigTheme(t.Context(), nil)
	require.Nil(t, rpcErr)
	require.Equal(t, persisted.UI.Theme, result.(map[string]string)["theme"])
}
