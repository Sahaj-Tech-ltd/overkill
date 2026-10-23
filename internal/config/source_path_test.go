package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadedProfileMigrationAndBootstrapKeepExplicitPath(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "bootstrap", true: "migration"}[existing], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OVERKILL_HOME", home)
			dir := t.TempDir()
			t.Chdir(dir)
			selected := "custom.toml"
			if existing {
				require.NoError(t, os.WriteFile(selected, []byte("version=0\n[ui]\ntheme='ocean'\n"), 0600))
			}
			cfg, err := Load(selected)
			require.NoError(t, err)
			path, err := cfg.PersistencePath()
			require.NoError(t, err)
			require.Equal(t, filepath.Join(dir, selected), path)
			_, err = os.Stat(filepath.Join(home, "config.toml"))
			require.True(t, os.IsNotExist(err))
			data, err := os.ReadFile(selected)
			require.NoError(t, err)
			require.NotContains(t, string(data), "sourcePath")
			require.NotContains(t, string(data), "source_path")
			if existing {
				require.Equal(t, CurrentVersion, cfg.Version)
				require.Contains(t, string(data), "version = 1")
				require.Equal(t, "ocean", cfg.UI.Theme)
			}
		})
	}
}
