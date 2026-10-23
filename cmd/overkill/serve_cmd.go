package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
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
instance, or explicitly opt into --local-database with --local-database-bin
pointing to installed PostgreSQL binaries. The server handles sessions, agents, and all TUI-facing
endpoints.`,
	RunE: runServe,
}

func runServe(cmd *cobra.Command, args []string) (resultErr error) {
	loadedCfg := cfg
	if loadedCfg == nil {
		loadedCfg = config.Default()
	}

	signalCtx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	databaseOwner, err := openCommandDatabase(signalCtx, cmd, loadedCfg, true)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, databaseOwner.Close()) }()
	database := databaseOwner.DB
	ctx := databaseOwner.Context

	log.Printf("connected to Postgres")

	// Session store (Postgres).
	sstore, err := session.NewPostgresStore(database)
	if err != nil {
		return fmt.Errorf("initialize session store: %w", err)
	}

	homeDir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	services, err := newRuntimeServices(loadedCfg, database, homeDir)
	if err != nil {
		return err
	}
	serverConfig := services.APIConfig(sstore)
	serverConfig.ListenAddr = serveAddr
	srv := api.NewServer(serverConfig)
	defer srv.Close()

	log.Printf("starting API server on %s", serveAddr)

	// Start gateway bots if any are enabled.
	gwHub, gwBots := newGatewayHub(loadedCfg, nil)
	if gwHub != nil {
		go func() {
			log.Printf("[gateway-hub] starting gateway bots")
			if err := gwHub.Run(ctx); err != nil {
				log.Printf("[gateway-hub] exited: %v", err)
			}
		}()
	}
	// Start completion-notify poller (§7.1 Layer 6).
	notifyCancel := startCompletionNotifyPoller(ctx, loadedCfg, log.Default(), gwBots)
	defer notifyCancel()

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
