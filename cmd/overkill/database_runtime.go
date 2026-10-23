package main

import (
	"context"
	"os"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/runtimedb"
	"github.com/spf13/cobra"
)

func addLocalDatabaseFlags(command *cobra.Command) {
	command.Flags().Bool("local-database", false, "Explicitly own a persistent local PostgreSQL cluster (requires installed binaries)")
	command.Flags().String("local-database-bin", "", "Absolute directory containing installed postgres, initdb, and pg_ctl; never downloaded")
	command.Flags().String("local-database-dir", "", "Absolute private cluster directory (default: OVERKILL_HOME/postgres)")
}

func commandDatabaseOptions(command *cobra.Command, loaded *config.Config, required bool) (runtimedb.Options, error) {
	if loaded == nil {
		loaded = config.Default()
	}
	home, err := config.ConfigDir()
	if err != nil {
		return runtimedb.Options{}, err
	}
	options := runtimedb.Options{URL: os.Getenv("DATABASE_URL"), Managed: loaded.LocalDatabase.Enabled, BinDir: loaded.LocalDatabase.BinDir, DataDir: loaded.LocalDatabase.DataDir, HomeDir: home, Required: required}
	if options.URL == "" {
		options.URL = loaded.DatabaseURL
	}
	if command.Flags().Changed("local-database") {
		options.Managed, _ = command.Flags().GetBool("local-database")
	}
	if command.Flags().Changed("local-database-bin") {
		options.BinDir, _ = command.Flags().GetString("local-database-bin")
	}
	if command.Flags().Changed("local-database-dir") {
		options.DataDir, _ = command.Flags().GetString("local-database-dir")
	}
	return options, nil
}

func openCommandDatabase(ctx context.Context, command *cobra.Command, loaded *config.Config, required bool) (*runtimedb.Owner, error) {
	options, err := commandDatabaseOptions(command, loaded, required)
	if err != nil {
		return nil, err
	}
	return runtimedb.Open(ctx, options)
}

func init() {
	for _, command := range []*cobra.Command{runCmd, tuiCmd, serveCmd, daemonStartCmd} {
		addLocalDatabaseFlags(command)
	}
}

// Warning resolution uses a snapshot: invocation flags must never be persisted.
func databaseWarningConfig(command *cobra.Command, loaded *config.Config) config.Config {
	snapshot := *loaded
	if command.Flags().Changed("local-database") {
		snapshot.LocalDatabase.Enabled, _ = command.Flags().GetBool("local-database")
	}
	return snapshot
}
