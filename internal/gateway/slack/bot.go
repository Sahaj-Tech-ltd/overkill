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

	// apiSem limits concurrent outbound API calls (PostMessage / UpdateMessage)
	// to avoid triggering Slack's tier-2 rate limit bursts.
	apiSem chan struct{}

	// runCtx carries the Run context for dispatching, so in-flight
	// handlers survive only as long as the bot is alive.
	runCtx   context.Context
	runCtxMu sync.Mutex
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
		apiSem:     make(chan struct{}, 3), // max 3 concurrent outbound API calls
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "slack" }

// Reconnect implements gateway.Reconnecter (B135). Socket Mode's
// WebSocket auto-reconnects; this is a stub for health monitors.
func (b *Bot) Reconnect(ctx context.Context) error { return nil }

// Run connects via Socket Mode and blocks until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	b.runCtxMu.Lock()
	b.runCtx = ctx
	b.runCtxMu.Unlock()

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
		// evt.Request may be nil for non-actionable events —
		// only Ack when the request envelope is populated.
		if evt.Request != nil {
			b.sm.Ack(*evt.Request)
		}

		switch inner := apiEvt.InnerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			b.onMessage(ctx, inner, apiEvt)
		case *slackevents.AppMentionEvent:
			// B134: Handle legacy bot app_mention events. These arrive
			// via the Events API (not Socket Mode) when the bot is
			// mentioned in a channel. Convert to a MessageEvent shape
			// so onMessage can process it without special casing.
			msg := &slackevents.MessageEvent{
				User:    inner.User,
				Text:    inner.Text,
				Channel: inner.Channel,
				TimeStamp: inner.TimeStamp,
			}
			b.onMessage(ctx, msg, apiEvt)
		}
	default:
		// evt.Request may be nil for lifecycle events.
		if evt.Request != nil {
			b.sm.Ack(*evt.Request)
		}
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

	// Strip bot @mention from beginning of message.
	// B133: Use ">" instead of "> " so no-space mentions
	// like "<@U123>please help" are also stripped.
	if strings.HasPrefix(text, "<@") {
		if idx := strings.Index(text, ">"); idx > 0 {
			text = strings.TrimSpace(text[idx+1:])
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
		Thread:   msg.ThreadTimeStamp,
		From:     from,
		Text:     text,
		IsDirect: isDirect,
	}

	reply := &slackReply{bot: b, client: b.Client, channel: msg.Channel, threadTS: msg.ThreadTimeStamp}
	b.runCtxMu.Lock()
	dispatchCtx := b.runCtx
	b.runCtxMu.Unlock()
	if dispatchCtx == nil {
		dispatchCtx = context.Background()
	}
	go b.Dispatcher.Handle(dispatchCtx, in, reply)
}

// slackReply implements gateway.Reply via Slack Web API.
type slackReply struct {
	bot     *Bot
	client  *slack.Client
	channel string
	// threadTS, when non-empty, makes PostInitial reply in-thread
	// instead of posting to the channel root.
	threadTS string

	mu sync.Mutex
	ts string // message timestamp for edits
}

func (r *slackReply) acquire() {
	if r.bot != nil && r.bot.apiSem != nil {
		r.bot.apiSem <- struct{}{}
	}
}

func (r *slackReply) release() {
	if r.bot != nil && r.bot.apiSem != nil {
		<-r.bot.apiSem
	}
}

func (r *slackReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
	r.acquire()
	defer r.release()
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if r.threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(r.threadTS))
	}
	_, ts, err := r.client.PostMessageContext(ctx, r.channel, opts...)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.ts = ts
	r.mu.Unlock()
	return ts, nil
}

func (r *slackReply) Update(ctx context.Context, handle, text string) error {
	r.acquire()
	defer r.release()
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
	r.acquire()
	defer r.release()
	_, _, postErr := r.client.PostMessageContext(ctx, r.channel, slack.MsgOptionText("⚠️ "+err.Error(), false))
	return postErr
}

func (r *slackReply) StartTyping() (stop func()) { return func() {} }
