// Package messaging provides cross-platform messaging tools for Overkill.
//
// Currently implements:
//   - send_message: send a message to any connected gateway platform
//     (Telegram, Discord, Slack).
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/slack-go/slack"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	tg "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/telegram"
)

// SendMessageTool implements tools.Tool for cross-platform messaging.
// Gateway clients are created lazily — a platform client is only
// instantiated on the first send request for that platform.
type SendMessageTool struct {
	cfg config.GatewayConfig

	mu        sync.Mutex
	tgClient  *tg.Client
	dgSession *discordgo.Session
	slClient  *slack.Client
}

// New creates a SendMessageTool backed by the gateway config.
func New(cfg config.GatewayConfig) *SendMessageTool {
	return &SendMessageTool{cfg: cfg}
}

func (t *SendMessageTool) Name() string { return "send_message" }

// SendInput is the JSON input for send_message.
type SendInput struct {
	Platform string `json:"platform"` // "telegram", "discord", "slack"
	Target   string `json:"target"`   // chat ID (string form) or channel ID
	Message  string `json:"message"`  // text to send
}

// SendOutput is the JSON output from send_message.
type SendOutput struct {
	Sent      bool   `json:"sent"`
	Platform  string `json:"platform"`
	Target    string `json:"target"`
	MessageID string `json:"message_id,omitempty"`
}

func (t *SendMessageTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in SendInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("send_message: invalid input: %w", err)
	}

	if in.Platform == "" {
		return nil, fmt.Errorf("send_message: platform is required (telegram, discord, slack)")
	}
	if in.Target == "" {
		return nil, fmt.Errorf("send_message: target is required")
	}
	if in.Message == "" {
		return nil, fmt.Errorf("send_message: message is required")
	}

	var msgID string
	var err error

	switch in.Platform {
	case "telegram":
		msgID, err = t.sendTelegram(ctx, in.Target, in.Message)
	case "discord":
		msgID, err = t.sendDiscord(ctx, in.Target, in.Message)
	case "slack":
		msgID, err = t.sendSlack(ctx, in.Target, in.Message)
	default:
		return nil, fmt.Errorf("send_message: unsupported platform %q (supported: telegram, discord, slack)", in.Platform)
	}

	if err != nil {
		return nil, fmt.Errorf("send_message: %s: %w", in.Platform, err)
	}

	out := SendOutput{
		Sent:      true,
		Platform:  in.Platform,
		Target:    in.Target,
		MessageID: msgID,
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("send_message: marshal output: %w", err)
	}
	return raw, nil
}

// --- Telegram backend ---

func (t *SendMessageTool) sendTelegram(ctx context.Context, target, text string) (string, error) {
	if t.cfg.Telegram.BotToken == "" {
		return "", fmt.Errorf("telegram: bot token not configured")
	}

	chatID, err := strconv.ParseInt(target, 10, 64)
	if err != nil {
		return "", fmt.Errorf("telegram: target must be a numeric chat ID, got %q: %w", target, err)
	}

	client := t.getTelegramClient()
	msgID, err := client.SendMessage(ctx, chatID, text)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(msgID), nil
}

func (t *SendMessageTool) getTelegramClient() *tg.Client {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tgClient == nil {
		t.tgClient = tg.New(t.cfg.Telegram.BotToken)
	}
	return t.tgClient
}

// --- Discord backend ---

func (t *SendMessageTool) sendDiscord(ctx context.Context, target, text string) (string, error) {
	if t.cfg.Discord.BotToken == "" {
		return "", fmt.Errorf("discord: bot token not configured")
	}

	sess, err := t.getDiscordSession()
	if err != nil {
		return "", err
	}

	msg, err := sess.ChannelMessageSend(target, text)
	if err != nil {
		return "", fmt.Errorf("discord: %w", err)
	}
	return msg.ID, nil
}

func (t *SendMessageTool) getDiscordSession() (*discordgo.Session, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dgSession == nil {
		sess, err := discordgo.New("Bot " + t.cfg.Discord.BotToken)
		if err != nil {
			return nil, fmt.Errorf("discord: create session: %w", err)
		}
		t.dgSession = sess
	}
	return t.dgSession, nil
}

// --- Slack backend ---

func (t *SendMessageTool) sendSlack(ctx context.Context, target, text string) (string, error) {
	if t.cfg.Slack.BotToken == "" {
		return "", fmt.Errorf("slack: bot token not configured")
	}

	client := t.getSlackClient()
	_, ts, err := client.PostMessageContext(ctx, target, slack.MsgOptionText(text, false))
	if err != nil {
		return "", fmt.Errorf("slack: %w", err)
	}
	return ts, nil
}

func (t *SendMessageTool) getSlackClient() *slack.Client {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.slClient == nil {
		t.slClient = slack.New(t.cfg.Slack.BotToken)
	}
	return t.slClient
}
