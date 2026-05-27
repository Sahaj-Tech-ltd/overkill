package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/bridge"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/discord"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/matrix"
	gwsignal "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/signal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/slack"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/telegram"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

var gatewayDryRun bool

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Run remote messaging gateways (Telegram, Discord, Slack, WhatsApp, Bridge, Signal, Matrix)",
	Long: `Pipes inbound messages from configured remote channels into the same
agent the TUI uses. Cross-channel session continuity: open the TUI,
step away, /follow tui from your phone, and your phone messages drive
whatever session the terminal is on.

Configure under [gateways.telegram] / [gateways.discord] / [gateways.slack] / [gateways.matrix]
in your config, or via env vars:
  TELEGRAM_BOT_TOKEN, DISCORD_BOT_TOKEN, SLACK_BOT_TOKEN, SLACK_APP_TOKEN, SIGNAL_ACCOUNT,
  MATRIX_ACCESS_TOKEN, MATRIX_PASSWORD`,

	RunE: runGateway,
}

func runGateway(cmd *cobra.Command, args []string) error {
	logger := log.New(os.Stderr, "[gateway] ", log.LstdFlags)
	if cfg == nil {
		return fmt.Errorf("gateway: no config loaded")
	}

	app := buildTUIApp()
	if app == nil || app.Agent == nil {
		return fmt.Errorf("gateway: no agent available — configure a provider first")
	}
	sender := &gatewayAgentAdapter{a: app.Agent}

	routerPath := ""
	if home, err := os.UserHomeDir(); err == nil {
		routerPath = filepath.Join(home, ".overkill", "gateway-sessions.json")
	}
	router, err := gateway.NewSessionRouter(routerPath)
	if err != nil {
		return err
	}
	disp := gateway.NewDispatcher(sender, router)
	disp.Logger = logger
	if v := buildVisionDescriber(cfg.Vision); v != nil {
		disp.Vision = v
		logger.Printf("vision: %s/%s wired for inbound images", cfg.Vision.Provider, cfg.Vision.Model)
	}
	if app.Tags != nil {
		disp.Bookmark = func(ctx context.Context, sessionID, label string) error {
			return app.Tags.Tag(sessionID, "bookmark/"+label, "gateway-bookmark")
		}
	}

	hub := gateway.NewHub()
	hub.Logger = logger

	var nb notifyBots

	// --- Telegram ---
	if t := cfg.Gateways.Telegram; t.Enabled || os.Getenv("TELEGRAM_BOT_TOKEN") != "" {
		token := t.BotToken
		if token == "" {
			token = os.Getenv("TELEGRAM_BOT_TOKEN")
		}
		if token == "" {
			logger.Printf("telegram: enabled but no token; skipping")
		} else {
			client := telegram.New(token)
			tb := telegram.NewBot(client, disp, t.AllowedChats)
			tb.Logger = logger
			hub.Add(tb)
			nb.telegramClient = client
			logger.Printf("telegram: registered (%d chat(s) on allow-list, 0 = all)", len(t.AllowedChats))
		}
	}

	// --- Discord ---
	if dc := cfg.Gateways.Discord; dc.Enabled || os.Getenv("DISCORD_BOT_TOKEN") != "" {
		token := dc.BotToken
		if token == "" {
			token = os.Getenv("DISCORD_BOT_TOKEN")
		}
		if token == "" {
			logger.Printf("discord: enabled but no token; skipping")
		} else {
			requireMention := true
			if os.Getenv("DISCORD_ALLOW_UNMENTIONED") != "" {
				requireMention = false
			}
			db := discord.NewBot(token, disp, dc.AllowedGuilds, dc.AllowedChannels, requireMention)
			db.Logger = logger
			hub.Add(db)
			nb.discordBot = db
			logger.Printf("discord: registered (%d guild(s), %d channel(s) on allow-list, 0 = any; mention required=%v)",
				len(dc.AllowedGuilds), len(dc.AllowedChannels), requireMention)
		}
	}

	// --- Slack ---
	if sl := cfg.Gateways.Slack; sl.Enabled || os.Getenv("SLACK_BOT_TOKEN") != "" {
		botToken := sl.BotToken
		if botToken == "" {
			botToken = os.Getenv("SLACK_BOT_TOKEN")
		}
		appToken := sl.AppToken
		if appToken == "" {
			appToken = os.Getenv("SLACK_APP_TOKEN")
		}
		if botToken == "" || appToken == "" {
			logger.Printf("slack: enabled but missing tokens; skipping")
		} else {
			sb := slack.NewBot(botToken, appToken, disp, sl.AllowedChannels)
			sb.Logger = logger
			hub.Add(sb)
			logger.Printf("slack: registered (socket mode)")
		}
	}

	// --- WhatsApp ---
	if wa := cfg.Gateways.WhatsApp; wa.Enabled {
		if err := registerWhatsApp(hub, disp, wa, logger, &nb); err != nil {
			logger.Printf("whatsapp: %v", err)
		}
	}

	// --- Bridge ---
	if br := cfg.Gateways.Bridge; br.Enabled {
		listen := br.Listen
		if listen == "" {
			listen = "127.0.0.1:7799"
		}
		b := bridge.New(disp, br.Token, listen)
		b.Logger = logger
		hub.Add(b)
		logger.Printf("bridge: registered on %s (auth=%v)", listen, br.Token != "")
	}

	// --- Signal ---
	if sg := cfg.Gateways.Signal; sg.Enabled {
		restURL := sg.RestAPIURL
		if restURL == "" {
			restURL = "http://localhost:8080"
		}
		acct := sg.Account
		if acct == "" {
			acct = os.Getenv("SIGNAL_ACCOUNT")
		}
		if acct == "" {
			logger.Printf("signal: enabled but no account; skipping")
		} else {
			sb := gwsignal.NewBot(restURL, acct, disp)
			sb.Logger = logger
			hub.Add(sb)
			logger.Printf("signal: registered (rest=%s, account=%s)", restURL, acct)
		}
	}

	// --- Matrix ---
	if mx := cfg.Gateways.Matrix; mx.Enabled || os.Getenv("MATRIX_ACCESS_TOKEN") != "" {
		hsURL := mx.HomeserverURL
		if hsURL == "" {
			hsURL = "https://matrix.org"
		}
		userID := mx.UserID
		token := mx.AccessToken
		if token == "" {
			token = os.Getenv("MATRIX_ACCESS_TOKEN")
		}
		password := mx.Password
		if password == "" {
			password = os.Getenv("MATRIX_PASSWORD")
		}
		if userID == "" && token == "" {
			logger.Printf("matrix: enabled but no user_id or access_token; skipping")
		} else {
			mb := matrix.NewBot(hsURL, userID, token, password, disp)
			mb.Logger = logger
			hub.Add(mb)
			logger.Printf("matrix: registered (homeserver=%s, user=%s)", hsURL, userID)
		}
	}

	if len(hub.Channels) == 0 {
		return fmt.Errorf("gateway: no channels enabled")
	}

	if gatewayDryRun {
		logger.Printf("dry-run: would start %d channel(s); exiting", len(hub.Channels))
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	notifyShutdown := startCompletionNotifyPoller(ctx, cfg, logger, nb)
	defer notifyShutdown()

	if err := hub.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func buildVisionDescriber(v config.VisionConfig) vision.Describer {
	if !v.Enabled {
		return nil
	}
	provider := v.Provider
	if provider == "" {
		provider = "anthropic"
	}
	switch provider {
	case "anthropic":
		key := v.APIKey
		if key == "" {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
		model := v.Model
		if model == "" {
			model = "claude-sonnet-4-5-20250929"
		}
		if key == "" {
			return nil
		}
		return vision.NewAnthropic(key, model)
	default:
		return nil
	}
}

type gatewayAgentAdapter struct{ a *agent.Agent }

func (g *gatewayAgentAdapter) Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error) {
	return g.a.Stream(ctx, in)
}
func (g *gatewayAgentAdapter) SetSessionID(id string) { g.a.SetSessionID(id) }
func (g *gatewayAgentAdapter) SessionID() string      { return g.a.SessionID() }
func (g *gatewayAgentAdapter) EStop()                 { g.a.EStop() }
func (g *gatewayAgentAdapter) Interrupt()             { g.a.Interrupt() }

func init() {
	gatewayCmd.Flags().BoolVar(&gatewayDryRun, "dry-run", false, "register channels and exit without polling")
	rootCmd.AddCommand(gatewayCmd)
}
