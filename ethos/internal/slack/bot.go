package slack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
)

// AgentSender is the agent-side interface the bot needs. Mirrors the shape
// used by internal/web — kept small so tests can drop in a fake.
type AgentSender interface {
	Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error)
	SetSessionID(id string)
	SessionID() string
}

// Bot wires a Socket-Mode connection, a Web API client, an ethos agent, and
// a per-thread session map together. One Bot per Slack workspace.
type Bot struct {
	API         SlackAPI
	Agent       AgentSender
	Sessions    *SessionMap
	AppToken    string
	APIBaseURL  string // override for tests; default https://slack.com/api
	Allowed     map[string]bool
	DryRun      bool
	Logger      *log.Logger
	UpdateEvery time.Duration // batch chat.update interval; default 500ms

	// SocketFactory builds the Socket Mode client — overridable so tests can
	// supply a stub that doesn't dial Slack. Returning nil opts the bot into
	// a "drain in-memory channel" path where Run pulls envelopes from
	// EnvelopeSource until the source closes.
	SocketFactory  func() *SocketClient
	EnvelopeSource <-chan *SocketEnvelope
	AckSink        func(envelopeID string) error // used in tests instead of WS write
}

// New returns a Bot with sensible defaults filled in.
func New(api SlackAPI, ag AgentSender, sessions *SessionMap, appToken string, allowed []string) *Bot {
	allowMap := map[string]bool{}
	for _, c := range allowed {
		allowMap[strings.TrimSpace(c)] = true
	}
	return &Bot{
		API:         api,
		Agent:       ag,
		Sessions:    sessions,
		AppToken:    appToken,
		Allowed:     allowMap,
		Logger:      log.New(io.Discard, "", 0),
		UpdateEvery: 500 * time.Millisecond,
	}
}

// Run drives the bot until ctx is cancelled or an unrecoverable error
// occurs. Network errors trigger reconnect with exponential backoff.
func (b *Bot) Run(ctx context.Context) error {
	if b.EnvelopeSource != nil {
		return b.runFromChannel(ctx)
	}
	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := b.runOnce(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		b.Logger.Printf("slack: socket error: %v (reconnect in %s)", err, backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (b *Bot) runOnce(ctx context.Context) error {
	var sc *SocketClient
	if b.SocketFactory != nil {
		sc = b.SocketFactory()
	}
	if sc == nil {
		sc = &SocketClient{APIBaseURL: b.APIBaseURL, AppToken: b.AppToken}
	}
	if err := sc.Connect(ctx); err != nil {
		return err
	}
	defer sc.Close()
	b.Logger.Printf("slack: socket connected")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		env, err := sc.ReadEnvelope(time.Now().Add(75 * time.Second))
		if err != nil {
			return err
		}
		b.handleEnvelope(ctx, env, sc.SendAck)
	}
}

func (b *Bot) runFromChannel(ctx context.Context) error {
	ack := b.AckSink
	if ack == nil {
		ack = func(string) error { return nil }
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case env, ok := <-b.EnvelopeSource:
			if !ok {
				return nil
			}
			b.handleEnvelope(ctx, env, ack)
		}
	}
}

func (b *Bot) handleEnvelope(ctx context.Context, env *SocketEnvelope, ack func(string) error) {
	if env == nil {
		return
	}
	switch env.Type {
	case "hello":
		// First frame after connect — nothing to ack.
		return
	case "disconnect":
		b.Logger.Printf("slack: server requested disconnect: %s", env.Reason)
		return
	case "events_api":
		// Always ack first; failure to handle the event later does not change
		// the ack — Slack just retries until it gets one.
		_ = ack(env.EnvelopeID)
		b.handleEvent(ctx, env.Payload.Event)
	default:
		_ = ack(env.EnvelopeID)
	}
}

func (b *Bot) handleEvent(ctx context.Context, ev Event) {
	if ev.IsFromBot() {
		return
	}
	switch ev.Type {
	case "app_mention":
		// fall through
	case "message":
		// Only auto-respond to DMs; channel messages need an @mention to avoid
		// every conversation summoning the bot.
		if ev.ChannelType != "im" {
			return
		}
	default:
		return
	}
	if !b.channelAllowed(ev.Channel) {
		b.Logger.Printf("slack: skipping message in disallowed channel %s", ev.Channel)
		return
	}
	if b.DryRun {
		b.Logger.Printf("slack: [dry-run] %s in %s: %q", ev.Type, ev.Channel, truncate(ev.Text, 80))
		return
	}
	go b.respond(ctx, ev)
}

func (b *Bot) channelAllowed(channel string) bool {
	if len(b.Allowed) == 0 {
		return true
	}
	return b.Allowed[channel]
}

// respond posts an initial "thinking" message, streams the agent reply, and
// updates the message in place. Errors are surfaced into the same message.
func (b *Bot) respond(ctx context.Context, ev Event) {
	threadAnchor := ev.ThreadAnchor()
	cleaned := stripMention(ev.Text)
	if cleaned == "" {
		return
	}

	// Reuse or create a session for the thread.
	if b.Sessions != nil {
		newID := newSessionID()
		sid, _, err := b.Sessions.GetOrCreate(ev.Channel, threadAnchor, newID)
		if err != nil {
			b.Logger.Printf("slack: session map: %v", err)
		}
		b.Agent.SetSessionID(sid)
	}

	postCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	msgTS, err := b.API.PostMessage(postCtx, ev.Channel, threadAnchor, ":hourglass: thinking…")
	cancel()
	if err != nil {
		b.Logger.Printf("slack: post initial: %v", err)
		return
	}
	_ = b.API.AddReaction(ctx, ev.Channel, msgTS, "hourglass")
	b.Logger.Printf("slack: posted reply ts=%s thread=%s", msgTS, threadAnchor)

	stream, err := b.Agent.Stream(ctx, cleaned)
	if err != nil {
		_ = b.API.UpdateMessage(ctx, ev.Channel, msgTS, ":x: error: "+EscapeMrkdwn(err.Error()))
		_ = b.API.RemoveReaction(ctx, ev.Channel, msgTS, "hourglass")
		return
	}
	b.streamToMessage(ctx, ev.Channel, msgTS, stream)
}

// streamToMessage drains the agent stream, batching token updates into
// chat.update calls at most once per UpdateEvery to respect Slack's ~1/sec
// per-message rate limit.
func (b *Bot) streamToMessage(ctx context.Context, channel, ts string, stream <-chan agent.StreamEvent) {
	var (
		mu        sync.Mutex
		buf       strings.Builder
		lastSent  string
		dirty     bool
		streamErr error
	)

	flush := func(force bool) {
		mu.Lock()
		current := buf.String()
		need := dirty && current != lastSent
		mu.Unlock()
		if !need && !force {
			return
		}
		text := MarkdownToMrkdwn(current)
		if text == "" {
			text = ":hourglass: thinking…"
		}
		fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := b.API.UpdateMessage(fctx, channel, ts, text)
		cancel()
		if err != nil {
			b.Logger.Printf("slack: chat.update: %v", err)
			return
		}
		mu.Lock()
		lastSent = current
		dirty = false
		mu.Unlock()
	}

	tick := time.NewTicker(b.UpdateEvery)
	defer tick.Stop()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for ev := range stream {
			switch ev.Type {
			case agent.EventToken:
				mu.Lock()
				buf.WriteString(ev.Content)
				dirty = true
				mu.Unlock()
			case agent.EventToolStart:
				if ev.ToolCall != nil {
					mu.Lock()
					if buf.Len() > 0 {
						buf.WriteString("\n\n")
					}
					buf.WriteString(FormatToolCall(ev.ToolCall.Name, ev.ToolCall.Arguments))
					dirty = true
					mu.Unlock()
				}
			case agent.EventToolOutput:
				// Single reaction per stream is enough — repeated tool calls
				// should not spam reactions either.
				_ = b.API.AddReaction(ctx, channel, ts, "white_check_mark")
			case agent.EventError:
				if ev.Error != nil {
					streamErr = ev.Error
				}
			case agent.EventDone:
				// Stream loop will exit on channel close; no extra action.
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			flush(false)
		case <-done:
			flush(true)
			_ = b.API.RemoveReaction(ctx, channel, ts, "hourglass")
			if streamErr != nil {
				msg := ":x: error: " + EscapeMrkdwn(streamErr.Error())
				_ = b.API.UpdateMessage(ctx, channel, ts, msg)
			}
			return
		}
	}
}

// stripMention removes the leading "<@UXXX>" tag Slack inserts in
// app_mention messages, leaving the user's actual text.
func stripMention(text string) string {
	t := strings.TrimSpace(text)
	if !strings.HasPrefix(t, "<@") {
		return t
	}
	end := strings.IndexByte(t, '>')
	if end < 0 {
		return t
	}
	return strings.TrimSpace(t[end+1:])
}

func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("slack-%d", time.Now().UnixNano())
	}
	return "slack-" + hex.EncodeToString(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
