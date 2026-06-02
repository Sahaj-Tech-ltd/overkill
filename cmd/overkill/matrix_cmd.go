package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	matrixbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/matrix"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
	"strings"

	_ "github.com/lib/pq"
)

var matrixCmd = &cobra.Command{
	Use:   "matrix",
	Short: "Run the Overkill Matrix bot (Element, etc.)",
	Long: `Connect overkill to Matrix via the Client-Server API.

Set Matrix config in [gateways.matrix] section or use env vars:
  MATRIX_HOMESERVER_URL (default https://matrix.org)
  MATRIX_USER_ID (@user:homeserver)
  MATRIX_ACCESS_TOKEN (or MATRIX_PASSWORD for auto-login)`,
	RunE: runMatrix,
}

func runMatrix(cmd *cobra.Command, args []string) error {
	logger := log.New(os.Stderr, "[matrix] ", log.LstdFlags)

	// Resolve provider.
	providerCfg, modelName := resolveProvider()
	if providerCfg == nil {
		providerCfg = detectProviderFromEnv()
	}
	if providerCfg == nil {
		return fmt.Errorf("no provider configured; set an API key env var or configure providers in config.toml")
	}

	apiKey := providerCfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(providerEnvVar(providerCfg.Name))
	}

	prov, err := providers.NewProvider(providers.FactoryConfig{
		Name:    providerCfg.Name,
		Type:    providerCfg.Type,
		APIKey:  apiKey,
		BaseURL: providerCfg.BaseURL,
		Headers: providerCfg.Headers,
	})
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	// Tool registry.
	cwd, _ := os.Getwd()
	toolReg := tools.NewDefaultRegistry(tools.FactoryDeps{
		CWD: cwd,
		OuroborosWall: func() *walls.OuroborosWall {
			if cfg.Ouroboros.Enabled {
				ouroCfg := cfg.Ouroboros
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

	logger.Printf("provider: %s, model: %s", providerCfg.Name, modelName)

	// Build agent.
	ag := agent.New(agent.Config{
		Provider:    prov,
		Tools:       toolReg,
		Compressors: tools.NewCompressorRegistry(),
		Hooks:       hooks.NewRegistry(),
		Scanners: []security.Scanner{
			security.NewCommandScanner(
				security.WithProjectPath(cwd),
			),
			security.NewInjectionScanner(),
		},
		Tokenizer:    tokenizer.NewEstimator(),
		Steering:     agent.NewSteeringQueue(agent.SteeringDrainAll),
		Model:        modelName,
		MaxTokens:    200000,
		SystemPrompt: buildSystemPrompt(cfg),
	})

	// Wire goal store and cost tracker with a shared database connection.
	var sharedDB *sql.DB
	if cfg != nil && cfg.DatabaseURL != "" {
		var dbErr error
		sharedDB, dbErr = sql.Open("postgres", cfg.DatabaseURL)
		if dbErr == nil {
			defer sharedDB.Close()
			if gs, gerr := agent.NewGoalStore(sharedDB); gerr == nil {
				ag.SetGoalStore(gs)
			} else {
				logger.Printf("goal store init failed: %v", gerr)
			}
		} else {
			logger.Printf("database open failed: %v (goal store disabled)", dbErr)
		}
	}

	// Gateway session router.
	homeDir, _ := config.ConfigDir()
	routerPath := filepath.Join(homeDir, "gateway-router.json")
	router, err := gateway.NewSessionRouter(routerPath)
	if err != nil {
		logger.Printf("warning: session router init: %v (continuing without persistence)", err)
		router = nil
	}

	// Gateway dispatcher.
	disp := gateway.NewDispatcher(ag, router)
	disp.Logger = logger

	// Wire cost tracker for /usage command (reuses shared DB).
	if sharedDB != nil {
		if ct, cerr := cost.NewPostgresTracker(sharedDB, cfg.Cost); cerr == nil {
			disp.CostTracker = ct
		} else {
			logger.Printf("cost tracker init failed: %v", cerr)
		}
	}

	// Matrix bot config.
	homeserverURL := os.Getenv("MATRIX_HOMESERVER_URL")
	userID := os.Getenv("MATRIX_USER_ID")
	accessToken := os.Getenv("MATRIX_ACCESS_TOKEN")
	password := os.Getenv("MATRIX_PASSWORD")

	if cfg != nil && cfg.Gateways.Matrix.Enabled {
		if cfg.Gateways.Matrix.HomeserverURL != "" {
			homeserverURL = cfg.Gateways.Matrix.HomeserverURL
		}
		if cfg.Gateways.Matrix.UserID != "" {
			userID = cfg.Gateways.Matrix.UserID
		}
		if cfg.Gateways.Matrix.AccessToken != "" {
			accessToken = cfg.Gateways.Matrix.AccessToken
		}
		if cfg.Gateways.Matrix.Password != "" {
			password = cfg.Gateways.Matrix.Password
		}
	}

	if homeserverURL == "" {
		homeserverURL = "https://matrix.org"
	}
	if accessToken == "" && password == "" {
		return fmt.Errorf("MATRIX_ACCESS_TOKEN or MATRIX_PASSWORD not set; pass via env var or [gateways.matrix] config")
	}

	bot := matrixbot.NewBot(homeserverURL, userID, accessToken, password, disp)
	bot.Logger = logger

	// Signal handling.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Printf("shutting down...")
		cancel()
	}()

	logger.Printf("starting Matrix bot (homeserver=%s, user=%s)", homeserverURL, userID)
	if err := bot.Run(ctx); err != nil {
		return fmt.Errorf("matrix bot: %w", err)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(matrixCmd)
}
