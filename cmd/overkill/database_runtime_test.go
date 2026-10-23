package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestCommandDatabaseResolution(t *testing.T) {
	const external = "postgres://overkill_test:local-audit-only@127.0.0.1:55432/overkill_test?sslmode=disable"
	t.Setenv("OVERKILL_HOME", t.TempDir())
	t.Setenv("DATABASE_URL", external)
	loaded := config.Default()
	loaded.DatabaseURL = "postgres://configured/overkill"
	loaded.LocalDatabase = config.LocalDatabaseConfig{Enabled: true, BinDir: "/configured/bin", DataDir: "/configured/data"}
	command := &cobra.Command{}
	addLocalDatabaseFlags(command)
	options, err := commandDatabaseOptions(command, loaded, true)
	require.NoError(t, err)
	require.Equal(t, external, options.URL)
	require.True(t, options.Managed)
	t.Setenv("DATABASE_URL", "")
	options, err = commandDatabaseOptions(command, loaded, true)
	require.NoError(t, err)
	require.Equal(t, loaded.DatabaseURL, options.URL)
	require.NoError(t, command.Flags().Set("local-database", "false"))
	require.NoError(t, command.Flags().Set("local-database-bin", "/explicit/bin"))
	require.NoError(t, command.Flags().Set("local-database-dir", "/explicit/data"))
	options, err = commandDatabaseOptions(command, loaded, false)
	require.NoError(t, err)
	require.False(t, options.Managed)
	require.False(t, options.Required)
	require.Equal(t, "/explicit/bin", options.BinDir)
	require.Equal(t, "/explicit/data", options.DataDir)
	require.True(t, loaded.LocalDatabase.Enabled, "flags must not mutate saved configuration")
}

func TestDatabaseCommandsExposeExplicitOptIn(t *testing.T) {
	for _, command := range []*cobra.Command{runCmd, tuiCmd, serveCmd, daemonStartCmd} {
		t.Run(command.CommandPath(), func(t *testing.T) {
			require.NotNil(t, command.Flags().Lookup("local-database"))
			require.NotNil(t, command.Flags().Lookup("local-database-bin"))
			require.NotNil(t, command.Flags().Lookup("local-database-dir"))
		})
	}
	t.Setenv("OVERKILL_HOME", t.TempDir())
	t.Setenv("DATABASE_URL", "")
	command := &cobra.Command{}
	addLocalDatabaseFlags(command)
	options, err := commandDatabaseOptions(command, nil, false)
	require.NoError(t, err)
	require.False(t, options.Managed)
	require.Equal(t, "", options.URL)
	require.Equal(t, "", options.DataDir)
	require.True(t, filepath.IsAbs(options.HomeDir))
}

func TestDatabaseWarningsRespectInvocationWithoutSaving(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	loaded := config.Default()
	loaded.DatabaseURL = ""
	command := &cobra.Command{}
	addLocalDatabaseFlags(command)
	require.NoError(t, command.Flags().Set("local-database", "true"))
	snapshot := databaseWarningConfig(command, loaded)
	require.True(t, snapshot.LocalDatabase.Enabled)
	require.False(t, loaded.LocalDatabase.Enabled)
	for _, warning := range snapshot.Warnings() {
		require.NotContains(t, string(warning), "database_url not configured")
	}
	loaded.LocalDatabase.Enabled = true
	require.NoError(t, command.Flags().Set("local-database", "false"))
	snapshot = databaseWarningConfig(command, loaded)
	require.False(t, snapshot.LocalDatabase.Enabled)
	require.True(t, loaded.LocalDatabase.Enabled)
	found := false
	for _, warning := range snapshot.Warnings() {
		if strings.Contains(string(warning), "database_url not configured") {
			found = true
		}
	}
	require.True(t, found)
}
