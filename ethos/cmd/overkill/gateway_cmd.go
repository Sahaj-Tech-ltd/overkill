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
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/telegram"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

var gatewayDryRun bool

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Run remote messaging gateways (Telegram + HTTP bridge for WhatsApp/Discord sidecars)",
	Long: `Pipes inbound messages from configured remote channels into the same
agent the TUI uses. Cross-channel session continuity: open the TUI,
step away, /follow tui from your phone, and your phone messages drive
whatever session the terminal is on.

Configure under [gateways.telegram] / [gateways.bridge] in your config,
or via TELEGRAM_BOT_TOKEN env var.`,
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
	// §7.4 bookmark wiring. /bm <label> from any gateway tags the
	// active session with a bookmark-prefixed label so the agent can
	// later recall it. We reuse the tui App's Tags manager when
	// present so bookmarks made from gateway + TUI land in the same
	// store. Nil-safe: Bookmark stays nil and the dispatcher surfaces
	// a clear error.
	if app.Tags != nil {
		disp.Bookmark = func(ctx context.Context, sessionID, label string) error {
			return app.Tags.Tag(sessionID, "bookmark/"+label, "gateway-bookmark")
		}
	}

	hub := gateway.NewHub()
	hub.Logger = logger

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
			logger.Printf("telegram: registered (%d chat(s) on allow-list, 0 = all)", len(t.AllowedChats))
		}
	}

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

	if len(hub.Channels) == 0 {
		return fmt.Errorf("gateway: no channels enabled — set [gateways.telegram] enabled = true or [gateways.bridge] enabled = true")
	}

	if gatewayDryRun {
		logger.Printf("dry-run: would start %d channel(s); exiting", len(hub.Channels))
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := hub.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

// buildVisionDescriber returns nil when vision is disabled or
// misconfigured. Today only the Anthropic provider is wired.
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

// gatewayAgentAdapter trims *agent.Agent down to gateway.AgentSender.
// Lives here so the gateway package never imports cmd/overkill.
type gatewayAgentAdapter struct{ a *agent.Agent }

func (g *gatewayAgentAdapter) Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error) {
	return g.a.Stream(ctx, in)
}
func (g *gatewayAgentAdapter) SetSessionID(id string) { g.a.SetSessionID(id) }
func (g *gatewayAgentAdapter) SessionID() string      { return g.a.SessionID() }

func init() {
	gatewayCmd.Flags().BoolVar(&gatewayDryRun, "dry-run", false, "register channels and exit without polling")
	rootCmd.AddCommand(gatewayCmd)
}
