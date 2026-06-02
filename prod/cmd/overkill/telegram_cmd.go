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
	telegrambot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/telegram"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
	"strings"

	_ "github.com/lib/pq"
)

var telegramCmd = &cobra.Command{
	Use:   "telegram",
	Short: "Run the Overkill Telegram bot",
	Long: `Connect overkill to Telegram using the Bot API.

Set Telegram config in [gateways.telegram] section or use env vars:
  TELEGRAM_BOT_TOKEN (bot token from @BotFather)
  TELEGRAM_ALLOWED_CHATS (comma-separated chat IDs; empty = any)`,
	RunE: runTelegram,
}

func runTelegram(cmd *cobra.Command, args []string) error {
	logger := log.New(os.Stderr, "[telegram] ", log.LstdFlags)

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

	// Telegram bot config.
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	var allowedChats []int64

	if cfg != nil && cfg.Gateways.Telegram.Enabled {
		if cfg.Gateways.Telegram.BotToken != "" {
			botToken = cfg.Gateways.Telegram.BotToken
		}
		allowedChats = cfg.Gateways.Telegram.AllowedChats
	}

	if botToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN not set; pass via env var or [gateways.telegram].bot_token in config")
	}

	client := telegrambot.New(botToken)
	bot := telegrambot.NewBot(client, disp, allowedChats)
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

	logger.Printf("starting Telegram bot")
	if err := bot.Run(ctx); err != nil {
		return fmt.Errorf("telegram bot: %w", err)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(telegramCmd)
}
