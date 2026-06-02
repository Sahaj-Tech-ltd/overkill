package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	discordbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/discord"
	matrixbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/matrix"
	mattermostbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/mattermost"
	signalbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/signal"
	slackbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/slack"
	telegrambot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/telegram"
	whatsappcloud "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/whatsapp/cloud"
	whatsappwm "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/whatsapp/whatsmeow"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
)

// daemonGatewayResult bundles the dispatcher + cleanups after starting
// configured gateway channels inside the daemon.
type daemonGatewayResult struct {
	Dispatcher *gateway.Dispatcher
}

// startDaemonGateways builds a minimal agent + dispatcher and starts
// all configured gateway channels. Returns the dispatcher so cron
// output can be routed through it. If no gateways are configured,
// returns nil dispatcher (shellOnFire fallback).
func startDaemonGateways(
	ctx context.Context,
	cfg *config.Config,
	database *sql.DB,
	activityFn func(),
) *daemonGatewayResult {
	if cfg == nil {
		return nil
	}

	// ── Provider ──
	providerCfg, modelName := resolveProvider()
	if providerCfg == nil {
		providerCfg = detectProviderFromEnv()
	}
	if providerCfg == nil {
		fmt.Fprintf(os.Stderr, "daemon: no provider configured — gateway dispatch disabled\n")
		return nil
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
		fmt.Fprintf(os.Stderr, "daemon: provider init failed: %v\n", err)
		return nil
	}

	// ── Agent (minimal) ──
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
	ag := agent.New(agent.Config{
		Provider:    prov,
		Tools:       toolReg,
		Compressors: tools.NewCompressorRegistry(),
		Hooks:       hooks.NewRegistry(),
		Scanners: []security.Scanner{
			security.NewCommandScanner(security.WithProjectPath(cwd)),
			security.NewInjectionScanner(),
		},
		Tokenizer: tokenizer.NewEstimator(),
		Steering:  agent.NewSteeringQueue(agent.SteeringDrainAll),
		Model:     modelName,
		MaxTokens: 200000,
		MaxSteps:  cfg.Agent.MaxTurns,
	})

	// Wire autonomy level from config when set.
	if cfg.Security.AutonomyLevel != "" {
		ag.SetAutoMode(cfg.Security.AutonomyLevel)
	}

	// ── Session router ──
	homeDir, _ := config.ConfigDir()
	routerPath := filepath.Join(homeDir, "gateway-router.json")
	router, err := gateway.NewSessionRouter(routerPath)
	if err != nil {
		router = nil
	}

	// ── Dispatcher ──
	disp := gateway.NewDispatcher(ag, router)
	disp.Logger = log.New(os.Stderr, "[gateway] ", log.LstdFlags)
	disp.ApplyGatewayLimits(gateway.GatewayLimitsCfg{
		MaxTextLen:      cfg.Gateways.MaxTextLen,
		MaxImageBytes:   cfg.Gateways.MaxImageBytes,
		RateLimitPerMin: cfg.Gateways.RateLimitPerMin,
		UpdateEveryMs:   cfg.Gateways.UpdateEveryMs,
	})
	if activityFn != nil {
		disp.OnActivity = activityFn
	}

	// ── Start configured channels ──
	if cfg.Gateways.Telegram.Enabled && cfg.Gateways.Telegram.BotToken != "" {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("gateway telegram: panic: %v", r)
				}
			}()
			client := telegrambot.New(cfg.Gateways.Telegram.BotToken)
			bot := telegrambot.NewBot(client, disp, cfg.Gateways.Telegram.AllowedChats)
			bot.Logger = log.New(os.Stderr, "[telegram] ", log.LstdFlags)
			fmt.Fprintf(os.Stderr, "daemon: starting Telegram bot\n")
			if err := bot.Run(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "daemon: telegram bot exited: %v\n", err)
			}
		}()
	}

	if cfg.Gateways.Discord.Enabled && cfg.Gateways.Discord.BotToken != "" {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("gateway discord: panic: %v", r)
				}
			}()
			bot := discordbot.NewBot(
				cfg.Gateways.Discord.BotToken, disp,
				cfg.Gateways.Discord.AllowedGuilds,
				cfg.Gateways.Discord.AllowedChannels,
				cfg.Gateways.Discord.RequireMention,
			)
			bot.Logger = log.New(os.Stderr, "[discord] ", log.LstdFlags)
			fmt.Fprintf(os.Stderr, "daemon: starting Discord bot\n")
			if err := bot.Run(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "daemon: discord bot exited: %v\n", err)
			}
		}()
	}

	// ── Slack ──
	if cfg.Gateways.Slack.Enabled {
		appToken := cfg.Gateways.Slack.AppToken
		if appToken == "" {
			appToken = os.Getenv("SLACK_APP_TOKEN")
		}
		botToken := cfg.Gateways.Slack.BotToken
		if botToken == "" {
			botToken = os.Getenv("SLACK_BOT_TOKEN")
		}
		if appToken != "" && botToken != "" {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("gateway slack: panic: %v", r)
					}
				}()
				bot := slackbot.NewBot(botToken, appToken, disp, cfg.Gateways.Slack.AllowedUsers, cfg.Gateways.Slack.AllowedChannels)
				bot.Logger = log.New(os.Stderr, "[slack] ", log.LstdFlags)
				fmt.Fprintf(os.Stderr, "daemon: starting Slack bot\n")
				if err := bot.Run(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "daemon: slack bot exited: %v\n", err)
				}
			}()
		} else {
			fmt.Fprintf(os.Stderr, "daemon: slack enabled but missing app_token or bot_token\n")
		}
	}

	// ── Signal ──
	if cfg.Gateways.Signal.Enabled {
		restURL := cfg.Gateways.Signal.RestAPIURL
		if restURL == "" {
			restURL = signalbot.DefaultRESTURL
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("gateway signal: panic: %v", r)
				}
			}()
			bot := signalbot.NewBot(restURL, cfg.Gateways.Signal.Account, cfg.Gateways.Signal.AuthToken, disp)
			bot.Logger = log.New(os.Stderr, "[signal] ", log.LstdFlags)
			if cfg.Gateways.Signal.SeenTTLSec > 0 {
				bot.SeenTTL = time.Duration(cfg.Gateways.Signal.SeenTTLSec) * time.Second
			}
			fmt.Fprintf(os.Stderr, "daemon: starting Signal bot\n")
			if err := bot.Run(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "daemon: signal bot exited: %v\n", err)
			}
		}()
	}

	// ── Matrix ──
	if cfg.Gateways.Matrix.Enabled {
		hsURL := cfg.Gateways.Matrix.HomeserverURL
		if hsURL == "" {
			hsURL = "https://matrix.org"
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("gateway matrix: panic: %v", r)
				}
			}()
			bot := matrixbot.NewBot(hsURL, cfg.Gateways.Matrix.UserID, cfg.Gateways.Matrix.AccessToken, cfg.Gateways.Matrix.Password, disp)
			bot.Logger = log.New(os.Stderr, "[matrix] ", log.LstdFlags)
			if cfg.Gateways.Matrix.SeenTTLSec > 0 {
				bot.SeenTTL = time.Duration(cfg.Gateways.Matrix.SeenTTLSec) * time.Second
			}
			if cfg.Gateways.Matrix.MemberCountTTLSec > 0 {
				bot.MemberCountTTL = time.Duration(cfg.Gateways.Matrix.MemberCountTTLSec) * time.Second
			}
			fmt.Fprintf(os.Stderr, "daemon: starting Matrix bot\n")
			if err := bot.Run(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "daemon: matrix bot exited: %v\n", err)
			}
		}()
	}

	// ── Mattermost ──
	if cfg.Gateways.Mattermost.Enabled && cfg.Gateways.Mattermost.BotToken != "" {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("gateway mattermost: panic: %v", r)
				}
			}()
			bot := mattermostbot.NewBot(
				cfg.Gateways.Mattermost.ServerURL,
				cfg.Gateways.Mattermost.BotToken,
				cfg.Gateways.Mattermost.TeamName,
				disp,
			)
			bot.Logger = log.New(os.Stderr, "[mattermost] ", log.LstdFlags)
			fmt.Fprintf(os.Stderr, "daemon: starting Mattermost bot\n")
			if err := bot.Run(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "daemon: mattermost bot exited: %v\n", err)
			}
		}()
	}

	// ── WhatsApp ──
	if cfg.Gateways.WhatsApp.Enabled {
		switch cfg.Gateways.WhatsApp.Backend {
		case "whatsmeow":
			storePath := cfg.Gateways.WhatsApp.Whatsmeow.StorePath
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("gateway whatsapp/whatsmeow: panic: %v", r)
					}
				}()
				bot := whatsappwm.NewBot(storePath, cfg.Gateways.WhatsApp.AllowedFrom, disp)
				bot.Logger = log.New(os.Stderr, "[whatsapp/whatsmeow] ", log.LstdFlags)
				fmt.Fprintf(os.Stderr, "daemon: starting WhatsApp (whatsmeow) bot\n")
				if err := bot.Run(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "daemon: whatsapp/whatsmeow bot exited: %v\n", err)
				}
			}()
		case "cloud":
			wc := cfg.Gateways.WhatsApp.Cloud
			if wc.PhoneNumberID != "" && wc.AccessToken != "" {
				listenAddr := wc.Listen
				if listenAddr == "" {
					listenAddr = config.DefaultWhatsAppCloudAddr
				}
				go func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("gateway whatsapp/cloud: panic: %v", r)
						}
					}()
					bot := whatsappcloud.NewBot(
						wc.PhoneNumberID, wc.AccessToken, wc.AppSecret, wc.VerifyToken,
						listenAddr, cfg.Gateways.WhatsApp.AllowedFrom, disp,
					)
					bot.Logger = log.New(os.Stderr, "[whatsapp/cloud] ", log.LstdFlags)
					if wc.SeenTTLSec > 0 {
						bot.SeenTTL = time.Duration(wc.SeenTTLSec) * time.Second
					}
					fmt.Fprintf(os.Stderr, "daemon: starting WhatsApp (cloud) bot\n")
					if err := bot.Run(ctx); err != nil {
						fmt.Fprintf(os.Stderr, "daemon: whatsapp/cloud bot exited: %v\n", err)
					}
				}()
			} else {
				fmt.Fprintf(os.Stderr, "daemon: whatsapp cloud enabled but missing phone_number_id or access_token\n")
			}
		default:
			fmt.Fprintf(os.Stderr, "daemon: whatsapp enabled but no backend set (whatsmeow|cloud)\n")
		}
	}

	return &daemonGatewayResult{Dispatcher: disp}
}
