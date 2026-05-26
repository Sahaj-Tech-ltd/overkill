package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	slackpkg "github.com/Sahaj-Tech-ltd/overkill/internal/slack"
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

	appToken, botToken := slackTokens()
	if appToken == "" || botToken == "" {
		fmt.Fprintln(os.Stderr, "overkill slack: missing tokens")
		fmt.Fprintln(os.Stderr, "  set [slack] app_token and bot_token in your config, or export")
		fmt.Fprintln(os.Stderr, "  SLACK_APP_TOKEN=xapp-... and SLACK_BOT_TOKEN=xoxb-...")
		fmt.Fprintln(os.Stderr, "  see docs/slack-app-manifest.yml for the Slack app manifest")
		return fmt.Errorf("slack tokens not configured")
	}

	if cfg != nil && !cfg.Slack.Enabled && !slackDryRun {
		// Tokens are present but the daemon is gated off; warn loudly so
		// users don't think they're connected.
		fmt.Fprintln(os.Stderr, "warning: [slack] enabled = false in config; set it to true to enable in production")
	}

	logger.Printf("starting (app=%s bot=%s dry-run=%v)",
		maskToken(appToken), maskToken(botToken), slackDryRun)

	app := buildTUIApp()
	if app == nil || app.Agent == nil {
		return fmt.Errorf("slack: no agent available — configure a provider first")
	}
	sender := &webAgentAdapter{a: app.Agent}

	channels := splitCSV(slackChannels)
	if len(channels) == 0 && cfg != nil {
		channels = cfg.Slack.AllowedChannels
	}

	sessionsPath := ""
	if home, err := os.UserHomeDir(); err == nil {
		sessionsPath = filepath.Join(home, ".overkill", "slack-sessions.json")
	}
	sm, err := slackpkg.NewSessionMap(sessionsPath)
	if err != nil {
		return fmt.Errorf("slack: load sessions: %w", err)
	}

	api := slackpkg.NewHTTPSlackAPI(botToken)
	bot := slackpkg.New(api, &slackAgentAdapter{sender}, sm, appToken, channels)
	bot.DryRun = slackDryRun
	bot.Logger = logger

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.Printf("ready: %d channel(s) on allow-list (empty = all where invited)", len(channels))
	if err := bot.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

// slackTokens resolves the App-Level and Bot tokens, preferring config over
// environment so users have one source of truth for production.
func slackTokens() (appToken, botToken string) {
	if cfg != nil {
		appToken = cfg.Slack.AppToken
		botToken = cfg.Slack.BotToken
	}
	if appToken == "" {
		appToken = os.Getenv("SLACK_APP_TOKEN")
	}
	if botToken == "" {
		botToken = os.Getenv("SLACK_BOT_TOKEN")
	}
	return
}

// maskToken returns a redacted form safe to log: prefix + last 4 chars.
// Tokens themselves are never written to disk or logs in full.
func maskToken(t string) string {
	if t == "" {
		return "<unset>"
	}
	if len(t) <= 8 {
		return "***"
	}
	prefix := t[:4]
	suffix := t[len(t)-4:]
	return prefix + "…" + suffix
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// slackAgentAdapter wraps the existing webAgentAdapter (which already trims
// *agent.Agent down to the Stream/SessionID surface) into the slack package's
// AgentSender interface. The two interfaces are identical in shape; we keep
// them separate so the slack package never imports internal/web.
type slackAgentAdapter struct{ inner *webAgentAdapter }

func (s *slackAgentAdapter) Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error) {
	return s.inner.Stream(ctx, in)
}
func (s *slackAgentAdapter) SetSessionID(id string) { s.inner.SetSessionID(id) }
func (s *slackAgentAdapter) SessionID() string      { return s.inner.SessionID() }

func init() {
	slackCmd.Flags().BoolVar(&slackDryRun, "dry-run", false, "log received events but do not reply")
	slackCmd.Flags().StringVar(&slackChannels, "channels", "", "comma-separated channel-ID allow-list (empty = all)")
	rootCmd.AddCommand(slackCmd)
}
