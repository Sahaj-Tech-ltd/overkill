// Package slack — Slack gateway via Socket Mode.
//
// Socket Mode uses a WebSocket connection authenticated with a Slack
// app-level token (xapp-). No public HTTP endpoint needed. Events
// arrive as JSON envelopes over the socket.
//
// Same shape as the Discord/Telegram bots: convert incoming events to
// gateway.Inbound, hand to dispatcher, format replies through
// gateway.Reply.
package slack

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

// Bot implements gateway.Channel for Slack Socket Mode.
type Bot struct {
	Client     *slack.Client
	Dispatcher *gateway.Dispatcher
	Allowed    map[string]bool // user IDs; empty = all
	Logger     *log.Logger

	sm *socketmode.Client
}

// NewBot returns a Bot wired to bot token + app token + dispatcher.
func NewBot(botToken, appToken string, d *gateway.Dispatcher, allowedUsers []string) *Bot {
	allow := make(map[string]bool)
	for _, u := range allowedUsers {
		allow[u] = true
	}
	api := slack.New(botToken, slack.OptionAppLevelToken(appToken))
	return &Bot{
		Client:     api,
		Dispatcher: d,
		Allowed:    allow,
		Logger:     log.New(io.Discard, "", 0),
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "slack" }

// Run connects via Socket Mode and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.sm = socketmode.New(b.Client)

	go func() {
		for evt := range b.sm.Events {
			b.handleSocketEvent(ctx, evt)
		}
	}()

	b.Logger.Printf("slack: socket mode connecting...")
	err := b.sm.RunContext(ctx)
	if err != nil && err != context.Canceled {
		return fmt.Errorf("slack: %w", err)
	}
	return ctx.Err()
}

func (b *Bot) handleSocketEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		b.Logger.Printf("slack: connecting...")
	case socketmode.EventTypeConnected:
		b.Logger.Printf("slack: connected")
	case socketmode.EventTypeEventsAPI:
		apiEvt, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		b.sm.Ack(*evt.Request)

		switch inner := apiEvt.InnerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			b.onMessage(ctx, inner, apiEvt)
		}
	default:
		b.sm.Ack(*evt.Request)
	}
}

func (b *Bot) onMessage(ctx context.Context, msg *slackevents.MessageEvent, apiEvt slackevents.EventsAPIEvent) {
	// Skip bot messages and subtypes (edits, deletes, etc.)
	if msg.BotID != "" || msg.SubType != "" {
		return
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	// Strip bot @mention from beginning of message
	if strings.HasPrefix(text, "<@") {
		if idx := strings.Index(text, "> "); idx > 0 {
			text = text[idx+2:]
		}
	}

	from := msg.User
	if len(b.Allowed) > 0 && !b.Allowed[from] {
		return
	}

	chatKey := msg.Channel
	isDirect := strings.HasPrefix(msg.Channel, "D")

	in := gateway.Inbound{
		Channel:  "slack",
		ChatKey:  chatKey,
		From:     from,
		Text:     text,
		IsDirect: isDirect,
	}

	reply := &slackReply{client: b.Client, channel: msg.Channel}
	go b.Dispatcher.Handle(context.Background(), in, reply)
}

// slackReply implements gateway.Reply via Slack Web API.
type slackReply struct {
	client  *slack.Client
	channel string

	mu sync.Mutex
	ts string // message timestamp for edits
}

func (r *slackReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
	_, ts, err := r.client.PostMessageContext(ctx, r.channel, slack.MsgOptionText(text, false))
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.ts = ts
	r.mu.Unlock()
	return ts, nil
}

func (r *slackReply) Update(ctx context.Context, handle, text string) error {
	if text == "" {
		text = "⏳ thinking…"
	}
	_, _, _, err := r.client.UpdateMessageContext(ctx, r.channel, handle, slack.MsgOptionText(text, false))
	return err
}

func (r *slackReply) Final(ctx context.Context, handle, text string) error {
	return r.Update(ctx, handle, text)
}

func (r *slackReply) Error(ctx context.Context, _ string, err error) error {
	_, _, postErr := r.client.PostMessageContext(ctx, r.channel, slack.MsgOptionText("⚠️ "+err.Error(), false))
	return postErr
}

func (r *slackReply) StartTyping() (stop func()) { return func() {} }
