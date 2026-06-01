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

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/slack"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
)

var (
	slackDryRun   bool
	slackChannels string
)

var slackCmd = &cobra.Command{
	Use:   "slack",
	Short: "Run the Overkill Slack bot daemon",
	Long: `Connect overkill to a Slack workspace using Socket Mode.

Reads tokens from config ([slack] section) or the SLACK_APP_TOKEN /
SLACK_BOT_TOKEN env vars. Disabled by default — set slack.enabled = true in
your config or pass tokens explicitly.

See docs/slack-app-manifest.yml for the Slack app manifest to install.`,
	RunE: runSlack,
}

func runSlack(cmd *cobra.Command, args []string) error {
	logger := log.New(os.Stderr, "[slack] ", log.LstdFlags)

	// ── Resolve tokens ──────────────────────────────────────────────
	appToken := os.Getenv("SLACK_APP_TOKEN")
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	var allowedChannels []string

	if cfg != nil && cfg.Slack.Enabled {
		if cfg.Slack.AppToken != "" {
			appToken = cfg.Slack.AppToken
		}
		if cfg.Slack.BotToken != "" {
			botToken = cfg.Slack.BotToken
		}
		if len(cfg.Slack.AllowedChannels) > 0 {
			allowedChannels = cfg.Slack.AllowedChannels
		}
	}

	if slackChannels != "" {
		allowedChannels = nil
		for _, c := range strings.Split(slackChannels, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				allowedChannels = append(allowedChannels, c)
			}
		}
	}

	if appToken == "" {
		return fmt.Errorf("SLACK_APP_TOKEN not set; pass it via env var or [slack].app_token in config")
	}
	if botToken == "" {
		return fmt.Errorf("SLACK_BOT_TOKEN not set; pass it via env var or [slack].bot_token in config")
	}

	// ── Resolve provider ────────────────────────────────────────────
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

	// ── Tools registry ──────────────────────────────────────────────
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

	sysPrompt := "You are Overkill, a vibe-coding agent with discipline.\nYou can run shell commands, read/write files, search code, and interact with git.\nBe concise and direct. Never guess URLs. Follow existing code conventions."
	if cfg != nil {
		sysPrompt = buildSystemPrompt(cfg)
	}

	// ── Agent ───────────────────────────────────────────────────────
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
		SystemPrompt: sysPrompt,
	})

	// ── Postgres session store ──────────────────────────────────────
	connString := os.Getenv("DATABASE_URL")
	if cfg != nil && cfg.DatabaseURL != "" {
		connString = cfg.DatabaseURL
	}
	if connString == "" {
		return fmt.Errorf("DATABASE_URL not set; required for Postgres backend")
	}

	database, err := db.Open(connString)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer database.Close()

	logger.Printf("connected to Postgres")

	// ── Slack API ───────────────────────────────────────────────────
	api := slack.NewHTTPSlackAPI(botToken)

	var sessions *slack.SessionMap
	if database != nil {
		sessions, err = slack.NewSessionMapDB(database, "slack_sessions")
	} else {
		sessions, err = slack.NewSessionMap("") // in-memory only; no persistence
	}
	if err != nil {
		return fmt.Errorf("session map: %w", err)
	}

	// ── Bot ─────────────────────────────────────────────────────────
	bot := slack.New(api, ag, sessions, appToken, allowedChannels)
	bot.Logger = logger
	bot.DryRun = slackDryRun

	// ── Signal handling ─────────────────────────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		logger.Printf("shutting down...")
		cancel()
	}()

	logger.Printf("starting Slack bot (dry-run=%v, channels=%d)", slackDryRun, len(allowedChannels))
	if err := bot.Run(ctx); err != nil {
		return fmt.Errorf("bot: %w", err)
	}

	return nil
}

func init() {
	slackCmd.Flags().BoolVar(&slackDryRun, "dry-run", false, "log received events but do not reply")
	slackCmd.Flags().StringVar(&slackChannels, "channels", "", "comma-separated channel-ID allow-list (empty = all)")
	rootCmd.AddCommand(slackCmd)
}
