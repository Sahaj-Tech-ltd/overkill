package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

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
)

// newGatewayHub builds a Hub and populated notifyBots with references to
// all enabled gateway bot instances. Returns (nil, empty) when no gateways
// are enabled.
func newGatewayHub(cfg *config.Config, d *gateway.Dispatcher) (*gateway.Hub, notifyBots) {
	var nb notifyBots
	if cfg == nil {
		return nil, nb
	}

	var channels []gateway.Channel

	// ── Telegram ──
	if cfg.Gateways.Telegram.Enabled {
		token := cfg.Gateways.Telegram.BotToken
		if token == "" {
			token = os.Getenv("TELEGRAM_BOT_TOKEN")
		}
		if token != "" {
			client := telegrambot.New(token)
			nb.telegramClient = client
			bot := telegrambot.NewBot(client, d, cfg.Gateways.Telegram.AllowedChats)
			channels = append(channels, bot)
			log.Printf("[gateway] telegram bot configured")
		} else {
			log.Printf("[gateway] telegram enabled but no bot token (set TELEGRAM_BOT_TOKEN)")
		}
	}

	// ── Discord ──
	if cfg.Gateways.Discord.Enabled {
		token := cfg.Gateways.Discord.BotToken
		if token == "" {
			token = os.Getenv("DISCORD_BOT_TOKEN")
		}
		if token != "" {
			bot := discordbot.NewBot(token, d,
				cfg.Gateways.Discord.AllowedGuilds,
				cfg.Gateways.Discord.AllowedChannels,
				cfg.Gateways.Discord.RequireMention,
			)
			if cfg.Gateways.Discord.CutoffSec > 0 {
				bot.EditCutoff = time.Duration(cfg.Gateways.Discord.CutoffSec) * time.Second
			}
			if cfg.Gateways.Discord.CleanupSec > 0 {
				bot.CleanupInterval = time.Duration(cfg.Gateways.Discord.CleanupSec) * time.Second
			}
			nb.discordNotifier = bot
			channels = append(channels, bot)
			log.Printf("[gateway] discord bot configured")
		}
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
			bot := slackbot.NewBot(botToken, appToken, d, cfg.Gateways.Slack.AllowedUsers, cfg.Gateways.Slack.AllowedChannels)
			nb.slackBot = bot
			channels = append(channels, bot)
			log.Printf("[gateway] slack bot configured")
		}
	}

	// ── Signal ──
	if cfg.Gateways.Signal.Enabled {
		restURL := cfg.Gateways.Signal.RestAPIURL
		if restURL == "" {
			restURL = signalbot.DefaultRESTURL
		}
		bot := signalbot.NewBot(restURL, cfg.Gateways.Signal.Account, cfg.Gateways.Signal.AuthToken, d)
		if cfg.Gateways.Signal.SeenTTLSec > 0 {
			bot.SeenTTL = time.Duration(cfg.Gateways.Signal.SeenTTLSec) * time.Second
		}
		nb.signalBot = bot
		channels = append(channels, bot)
		log.Printf("[gateway] signal bot configured")
	}

	// ── Matrix ──
	if cfg.Gateways.Matrix.Enabled {
		hsURL := cfg.Gateways.Matrix.HomeserverURL
		if hsURL == "" {
			hsURL = "https://matrix.org"
		}
		bot := matrixbot.NewBot(hsURL, cfg.Gateways.Matrix.UserID, cfg.Gateways.Matrix.AccessToken, cfg.Gateways.Matrix.Password, d)
		if cfg.Gateways.Matrix.SeenTTLSec > 0 {
			bot.SeenTTL = time.Duration(cfg.Gateways.Matrix.SeenTTLSec) * time.Second
		}
		if cfg.Gateways.Matrix.MemberCountTTLSec > 0 {
			bot.MemberCountTTL = time.Duration(cfg.Gateways.Matrix.MemberCountTTLSec) * time.Second
		}
		nb.matrixBot = bot
		channels = append(channels, bot)
		log.Printf("[gateway] matrix bot configured")
	}

	// ── Mattermost ──
	if cfg.Gateways.Mattermost.Enabled {
		bot := mattermostbot.NewBot(
			cfg.Gateways.Mattermost.ServerURL,
			cfg.Gateways.Mattermost.BotToken,
			cfg.Gateways.Mattermost.TeamName,
			d,
		)
		nb.mattermostNotifier = bot
		channels = append(channels, bot)
		log.Printf("[gateway] mattermost bot configured")
	}

	// ── WhatsApp ──
	if cfg.Gateways.WhatsApp.Enabled {
		switch cfg.Gateways.WhatsApp.Backend {
		case "whatsmeow":
			storePath := cfg.Gateways.WhatsApp.Whatsmeow.StorePath
			bot := whatsappwm.NewBot(storePath, cfg.Gateways.WhatsApp.AllowedFrom, d)
			nb.whatsmeowNotifier = bot
			channels = append(channels, bot)
			log.Printf("[gateway] whatsapp (whatsmeow) bot configured")
		case "cloud":
			wc := cfg.Gateways.WhatsApp.Cloud
			bot := whatsappcloud.NewBot(
				wc.PhoneNumberID, wc.AccessToken, wc.AppSecret, wc.VerifyToken,
				wc.Listen, cfg.Gateways.WhatsApp.AllowedFrom, d,
			)
			if cfg.Gateways.WhatsApp.Cloud.SeenTTLSec > 0 {
				bot.SeenTTL = time.Duration(cfg.Gateways.WhatsApp.Cloud.SeenTTLSec) * time.Second
			}
			nb.whatsappCloudNotifier = bot
			channels = append(channels, bot)
			log.Printf("[gateway] whatsapp (cloud) bot configured")
		default:
			log.Printf("[gateway] whatsapp enabled but no backend set")
		}
	}

	if len(channels) == 0 {
		return nil, nb
	}
	hub := gateway.NewHub(channels...)
	// Wire Hub backoff config.
	if cfg.Gateways.BackoffInitialSec > 0 {
		hub.BackoffInitial = time.Duration(cfg.Gateways.BackoffInitialSec) * time.Second
	}
	if cfg.Gateways.BackoffMaxSec > 0 {
		hub.BackoffMax = time.Duration(cfg.Gateways.BackoffMaxSec) * time.Second
	}
	return hub, nb
}

// gatewayNotifier sends a message to every gateway with a notify target (§7.1 Layer 6).
type gatewayNotifier struct {
	cfg *config.GatewayConfig
}

func newGatewayNotifier(cfg *config.GatewayConfig) *gatewayNotifier {
	return &gatewayNotifier{cfg: cfg}
}

func (n *gatewayNotifier) Send(ctx context.Context, msg string) {
	if n.cfg == nil {
		return
	}

	// Telegram
	tg := n.cfg.Telegram
	if tg.Enabled && tg.NotifyChatID != 0 {
		token := tg.BotToken
		if token == "" {
			token = os.Getenv("TELEGRAM_BOT_TOKEN")
		}
		if token != "" {
			go func() {
				client := telegrambot.New(token)
				if _, err := client.SendMessage(ctx, tg.NotifyChatID, msg); err != nil {
					log.Printf("[notifier] telegram: %v", err)
				}
			}()
		}
	}

	// Discord
	dc := n.cfg.Discord
	if dc.Enabled && dc.NotifyChannelID != "" {
		token := dc.BotToken
		if token == "" {
			token = os.Getenv("DISCORD_BOT_TOKEN")
		}
		if token != "" {
			go func() {
				if err := sendDiscordMsg(token, dc.NotifyChannelID, msg); err != nil {
					log.Printf("[notifier] discord: %v", err)
				}
			}()
		}
	}
}

func sendDiscordMsg(token, channelID, content string) error {
	url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", channelID)
	body := fmt.Sprintf(`{"content":%q}`, content)
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord: HTTP %d", resp.StatusCode)
	}
	return nil
}
