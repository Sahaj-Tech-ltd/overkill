package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

var gatewayDryRun bool

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Run all configured messaging gateways",
	Long: `Starts configured remote messaging bots.

Use individual bot commands for now:
  overkill telegram   — Telegram bot
  overkill discord    — Discord bot
  overkill slack      — Slack bot
  overkill whatsapp   — WhatsApp bot
  overkill mattermost — Mattermost bot
  overkill bridge     — HTTP sidecar bridge (SMS, iMessage, etc.)

The unified gateway (all bots in one process) is being wired
now that Postgres is available.`,

	RunE: runGateway,
}

func runGateway(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("gateway: config: %w", err)
	}

	var available []string
	if cfg.Gateways.Telegram.BotToken != "" {
		available = append(available, "telegram")
	}
	if cfg.Gateways.Discord.BotToken != "" {
		available = append(available, "discord")
	}
	if cfg.Gateways.Slack.BotToken != "" {
		available = append(available, "slack")
	}
	if cfg.Gateways.Signal.Enabled && cfg.Gateways.Signal.Account != "" {
		available = append(available, "signal")
	}
	if cfg.Gateways.Matrix.Enabled {
		available = append(available, "matrix")
	}
	if cfg.Gateways.Mattermost.Enabled && cfg.Gateways.Mattermost.BotToken != "" {
		available = append(available, "mattermost")
	}
	if cfg.Gateways.Bridge.Enabled {
		available = append(available, "bridge (sidecar gateway)")
	}

	if len(available) == 0 {
		fmt.Println("No gateways configured. Set bot tokens in config under [gateways.*].")
		return nil
	}

	fmt.Printf("Configured gateways: %s\n", strings.Join(available, ", "))
	fmt.Println("All configured gateways are now available as individual commands:")
	for _, g := range available {
		fmt.Printf("  overkill %s\n", g)
	}
	return nil
}

func init() {
	gatewayCmd.Flags().BoolVar(&gatewayDryRun, "dry-run", false, "register channels and exit without polling")
	rootCmd.AddCommand(gatewayCmd)
}
