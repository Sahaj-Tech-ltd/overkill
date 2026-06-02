package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
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
		// OuroborosWall — adversarial code review wall (§6.5).
		// Uses a separate provider from the main agent so reviews are independent.
		OuroborosWall: func() *walls.OuroborosWall {
			if loadedCfg != nil && loadedCfg.Ouroboros.Enabled {
				ouroCfg := loadedCfg.Ouroboros
				ouroKey := ouroCfg.APIKey
				if ouroKey == "" {
					ouroKey = os.Getenv(strings.ToUpper(ouroCfg.Provider) + "_API_KEY")
				}
				if ouroKey != "" && ouroCfg.Provider != "" {
					ouroProv, err := providers.NewProvider(providers.FactoryConfig{
						Name:    ouroCfg.Provider,
						Type:    ouroCfg.Provider,
						APIKey:  ouroKey,
						BaseURL: ouroCfg.BaseURL,
					})
					if err == nil {
						return walls.NewOuroborosWall(walls.OuroborosConfig{
							Enabled:      true,
							Provider:     ouroProv,
							Model:        ouroCfg.Model,
							StrictMode:   ouroCfg.StrictMode,
							SystemPrompt: ouroCfg.SystemPrompt,
						})
					}
				}
			}
			return walls.NewOuroborosWall(walls.OuroborosConfig{})
		}(),
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
		ListenAddr:   serveAddr,
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
