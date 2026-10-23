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
	signalbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/signal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
	"strings"

	_ "github.com/lib/pq"
)

var signalCmd = &cobra.Command{
	Use:   "signal",
	Short: "Run the Overkill Signal bot via signal-cli REST API",
	Long: `Connect overkill to Signal using signal-cli's REST API mode.

Requires signal-cli running as a daemon with --rest-api.
Set Signal config in [gateways.signal] section or use env vars:
  SIGNAL_REST_API_URL (default http://localhost:8080)
  SIGNAL_ACCOUNT (E.164 phone number)
  SIGNAL_AUTH_TOKEN (Bearer token for signal-cli auth)`,
	RunE: runSignal,
}

func runSignal(cmd *cobra.Command, args []string) error {
	logger := log.New(os.Stderr, "[signal] ", log.LstdFlags)

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
		Models:  providerCfg.ProviderModels(),
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
	limits := providers.TokenLimitsFor(prov.Models(), cfg.Agent.MaxOutputTokens)
	contextWindow, outputTokens, err := limits(modelName)
	if err != nil {
		return fmt.Errorf("agent token limits: %w", err)
	}
	hookRegistry, err := loadConfiguredHooks()
	if err != nil {
		return fmt.Errorf("load user hooks: %w", err)
	}
	ag := agent.New(agent.Config{
		Provider:    prov,
		Tools:       toolReg,
		Compressors: tools.NewCompressorRegistry(),
		Hooks:       hookRegistry,
		Scanners: []security.Scanner{
			security.NewCommandScanner(
				security.WithProjectPath(cwd),
			),
			security.NewInjectionScanner(),
		},
		Tokenizer:     tokenizer.NewEstimator(),
		Steering:      agent.NewSteeringQueue(agent.SteeringDrainAll),
		Model:         modelName,
		MaxTokens:     outputTokens,
		ContextWindow: contextWindow,
		TokenLimits:   limits,
		SystemPrompt:  buildSystemPrompt(cfg),
	})
	defer ag.Shutdown()

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
			return fmt.Errorf("signal: open configured database: %w", dbErr)
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

	// A configured usage database must initialize successfully; only the
	// deliberate no-database mode may start without a cost tracker.
	if sharedDB != nil {
		if ct, cerr := cost.NewPostgresTracker(sharedDB, cfg.Cost); cerr == nil {
			disp.CostTracker = ct
		} else {
			return fmt.Errorf("signal: initialize cost tracker: %w", cerr)
		}
	}

	// Signal bot config.
	restAPIURL := os.Getenv("SIGNAL_REST_API_URL")
	account := os.Getenv("SIGNAL_ACCOUNT")
	authToken := os.Getenv("SIGNAL_AUTH_TOKEN")

	if cfg != nil && cfg.Gateways.Signal.Enabled {
		if cfg.Gateways.Signal.RestAPIURL != "" {
			restAPIURL = cfg.Gateways.Signal.RestAPIURL
		}
		if cfg.Gateways.Signal.Account != "" {
			account = cfg.Gateways.Signal.Account
		}
		if cfg.Gateways.Signal.AuthToken != "" {
			authToken = cfg.Gateways.Signal.AuthToken
		}
	}

	if restAPIURL == "" {
		restAPIURL = signalbot.DefaultRESTURL
	}
	if account == "" {
		return fmt.Errorf("SIGNAL_ACCOUNT not set; pass it via env var or [gateways.signal].account in config")
	}

	bot := signalbot.NewBot(restAPIURL, account, authToken, disp)
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

	logger.Printf("starting Signal bot (rest=%s, account=%s)", restAPIURL, account)
	if err := bot.Run(ctx); err != nil {
		return fmt.Errorf("signal bot: %w", err)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(signalCmd)
}
