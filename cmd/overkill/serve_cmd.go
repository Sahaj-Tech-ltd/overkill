package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

var (
	serveWorkers int
	serveAddr    string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start overkill API server with Postgres backend",
	Long: `serve starts the ACP HTTP server backed by Postgres.

The API server exposes the same JSON-RPC interface the TUI uses. Set
DATABASE_URL or database_url in config.toml to point at a Postgres
instance. The server handles sessions, agents, and all TUI-facing
endpoints.`,
	RunE: runServe,
}

func runServe(cmd *cobra.Command, args []string) error {
	loadedCfg := cfg
	if loadedCfg == nil {
		loadedCfg = config.Default()
	}

	// Resolve database connection string.
	connString := loadedCfg.DatabaseURL
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}
	if connString == "" {
		return fmt.Errorf("DATABASE_URL required for Postgres backend — set it in ~/.overkill/config.toml or the environment")
	}

	// Open Postgres and run migrations.
	database, err := db.Open(connString)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		return fmt.Errorf("db migrate: %w", err)
	}

	log.Printf("connected to Postgres")

	// Session store (Postgres).
	sstore := session.NewPostgresStore(database)

	// Tool registry.
	cwd, _ := os.Getwd()
	toolReg := tools.NewDefaultRegistry(tools.FactoryDeps{
		CWD: cwd,
	})

	// Cost tracker — powers session.usage RPC.
	var costTracker cost.Tracker
	if ct, cerr := cost.NewPostgresTracker(database, loadedCfg.Cost); cerr == nil {
		costTracker = ct
	} else {
		log.Printf("cost tracker init failed: %v (usage tracking disabled)", cerr)
	}

	// Build the API server.
	srv := api.NewServer(api.ServerConfig{
		Config:       loadedCfg,
		SessionStore: sstore,
		Tools:        toolReg,
		CostTracker:  costTracker,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("shutting down API server...")
		cancel()
	}()

	log.Printf("starting API server on %s", serveAddr)

	// Start blocks until context is cancelled.
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func init() {
	serveCmd.Flags().IntVar(&serveWorkers, "workers", 4, "number of concurrent job workers")
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1:7777", "host:port for the ACP server")
	rootCmd.AddCommand(serveCmd)
}
