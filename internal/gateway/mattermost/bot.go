// Package mattermost implements a Mattermost messaging gateway via
// the Mattermost WebSocket API (wss://server/api/v4/websocket) for
// real-time events and the REST API for posting messages.
//
// Receive: WebSocket connection subscribes to posted events from
// channels the bot belongs to. Filters out own messages and dispatches
// them as gateway.Inbound.
//
// Send: POST /api/v4/posts with message and channel_id. PostInitial
// sends the dispatcher-supplied text; Update is a no-op (Mattermost
// doesn't support editing messages in place); Final is also a no-op
// after the initial post.
//
// Health: checks WebSocket connection is alive via periodic ping/pong.
package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

var _ gateway.Notifier = (*Bot)(nil)

// Bot implements gateway.Channel for Mattermost. One Bot per token.
type Bot struct {
	ServerURL  string // e.g. https://mattermost.example.com
	BotToken   string
	TeamName   string
	Dispatcher *gateway.Dispatcher
	Logger     *log.Logger

	// AllowedChannels restricts inbound messages to specific channel IDs.
	// Empty map (default) means all channels are allowed.
	AllowedChannels map[string]bool
	// AllowedUsers restricts inbound messages to specific user IDs.
	// Empty map (default) means all users are allowed.
	AllowedUsers map[string]bool

	// httpClient is used for all REST API calls (posts, user lookup).
	// Created with a 30s timeout in NewBot to avoid hanging forever.
	httpClient *http.Client

	// userID is the bot's own user ID, set after WebSocket hello.
	userID string

	mu     sync.Mutex
	closed bool

	// runCtx carries the Run context for dispatching.
	runCtx   context.Context
	runCtxMu sync.Mutex

	// Health / reconnect state.
	healthMu     sync.Mutex
	connected    bool
	lastPong     time.Time
	backoffCount int
}

// NewBot returns a Bot wired to a server URL, token, and dispatcher.
func NewBot(serverURL, botToken, teamName string, d *gateway.Dispatcher) *Bot {
	return &Bot{
		ServerURL:  strings.TrimRight(serverURL, "/"),
		BotToken:   botToken,
		TeamName:   teamName,
		Dispatcher: d,
		Logger:     log.New(io.Discard, "", 0),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "mattermost" }

// Healthy reports whether the WebSocket is connected and received a
// pong within the last 60 seconds.
func (b *Bot) Healthy() bool {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()
	if !b.connected {
		return false
	}
	return time.Since(b.lastPong) < 60*time.Second
}

// Run opens the Mattermost WebSocket connection and blocks until ctx
// is cancelled. On disconnect the bot reconnects with exponential
// backoff (1s, 2s, 4s, 8s, 16s, 30s cap). Returns ctx.Err() on cancel.
func (b *Bot) Run(ctx context.Context) error {
	b.runCtxMu.Lock()
	b.runCtx = ctx
	b.runCtxMu.Unlock()

	return b.connectLoop(ctx)
}

// connectLoop connects the WebSocket, reads events, and reconnects on
// disconnect with exponential backoff.
func (b *Bot) connectLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wsURL := strings.Replace(b.ServerURL, "https://", "wss://", 1)
		wsURL = strings.Replace(wsURL, "http://", "ws://", 1)
		wsURL += "/api/v4/websocket"

		b.Logger.Printf("mattermost: connecting to %s", wsURL)

		dialer := websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
		}
		header := http.Header{}
		header.Set("Authorization", "Bearer "+b.BotToken)

		conn, _, err := dialer.DialContext(ctx, wsURL, header)
		if err != nil {
			b.Logger.Printf("mattermost: connect: %v", err)
			delay := b.backoff()
			b.Logger.Printf("mattermost: backoff %v before reconnect", delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		b.healthMu.Lock()
		b.connected = true
		b.lastPong = time.Now()
		b.backoffCount = 0
		b.healthMu.Unlock()

		b.Logger.Printf("mattermost: connected")

		// Fetch our own user ID so we can filter out our own messages.
		if err := b.fetchOwnUser(ctx); err != nil {
			b.Logger.Printf("mattermost: fetch own user: %v", err)
		}

		// Read loop handles incoming frames.
		if err := b.readLoop(ctx, conn); err != nil {
			b.Logger.Printf("mattermost: read loop: %v", err)
		}

		b.healthMu.Lock()
		b.connected = false
		b.healthMu.Unlock()

		delay := b.backoff()
		b.Logger.Printf("mattermost: disconnected, backoff %v before reconnect", delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// backoff returns the current backoff duration and advances the counter.
func (b *Bot) backoff() time.Duration {
	b.healthMu.Lock()
	count := b.backoffCount
	b.backoffCount++
	b.healthMu.Unlock()

	// Cap at 30 to prevent overflow.
	if count > 30 {
		count = 30
	}
	d := time.Duration(1<<count) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// wsEvent represents a Mattermost WebSocket event.
type wsEvent struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data"`
	Broadcast json.RawMessage `json:"broadcast"`
	Seq       int64           `json:"seq"`
}

// wsHello is the hello event data sent on connection.
type wsHello struct {
	ServerVersion string `json:"server_version"`
}

// wsPosted is a subset of the posted event data we care about.
type wsPosted struct {
	Post        string `json:"post"` // JSON-encoded post
	ChannelID   string `json:"channel_id"`
	ChannelType string `json:"channel_type"` // "D" for direct, "O" for public, "P" for private
}

// post is the Mattermost Post object (subset).
type post struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id"`
	Message   string `json:"message"`
	Type      string `json:"type"`
}

// readLoop reads WebSocket frames from the connection and dispatches events.
func (b *Bot) readLoop(ctx context.Context, conn *websocket.Conn) error {
	defer conn.Close()

	// Set a read deadline so we can periodically check ctx cancellation.
	// Mattermost sends a ping every ~30s; 60s read deadline is safe.
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var event wsEvent
		if err := conn.ReadJSON(&event); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("mattermost: read frame: %w", err)
		}

		// Reset the read deadline on each successful read.
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		b.healthMu.Lock()
		b.lastPong = time.Now()
		b.healthMu.Unlock()

		switch event.Event {
		case "hello":
			var hello wsHello
			if err := json.Unmarshal(event.Data, &hello); err == nil {
				b.Logger.Printf("mattermost: server version %s", hello.ServerVersion)
			}

		case "posted":
			b.handlePosted(ctx, event)

		case "ping":
			// Reply with pong to keep the connection alive.
			if err := conn.WriteMessage(websocket.PongMessage, nil); err != nil {
				b.Logger.Printf("mattermost: pong write: %v", err)
			}
		}
	}
}

// handlePosted processes an incoming message event.
func (b *Bot) handlePosted(ctx context.Context, event wsEvent) {
	var data wsPosted
	if err := json.Unmarshal(event.Data, &data); err != nil {
		b.Logger.Printf("mattermost: unmarshal posted: %v", err)
		return
	}

	// Parse the nested post JSON.
	var p post
	if err := json.Unmarshal([]byte(data.Post), &p); err != nil {
		b.Logger.Printf("mattermost: unmarshal post: %v", err)
		return
	}

	// Skip our own messages.
	if p.UserID == b.userID {
		return
	}

	// Skip system messages.
	if p.Type != "" && p.Type != "system_generic" {
		return
	}

	text := strings.TrimSpace(p.Message)
	if text == "" {
		return
	}

	// Allowlist checks: if configured, only accept messages from
	// allowed channels and users.
	if len(b.AllowedChannels) > 0 && !b.AllowedChannels[p.ChannelID] {
		b.Logger.Printf("mattermost: skip disallowed channel %s", p.ChannelID)
		return
	}
	if len(b.AllowedUsers) > 0 && !b.AllowedUsers[p.UserID] {
		b.Logger.Printf("mattermost: skip disallowed user %s", p.UserID)
		return
	}

	isDirect := data.ChannelType == "D"

	in := gateway.Inbound{
		Channel:  "mattermost",
		ChatKey:  p.ChannelID,
		From:     p.UserID,
		Text:     text,
		IsDirect: isDirect,
	}

	reply := &mattermostReply{bot: b, channelID: p.ChannelID}

	b.runCtxMu.Lock()
	dispatchCtx := b.runCtx
	b.runCtxMu.Unlock()
	if dispatchCtx == nil {
		dispatchCtx = context.Background()
	}
	go b.Dispatcher.Handle(dispatchCtx, in, reply)
}

// postMessage sends a message to a channel via the REST API.
func (b *Bot) postMessage(ctx context.Context, channelID, text string) (string, error) {
	body := map[string]interface{}{
		"channel_id": channelID,
		"message":    text,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("mattermost: marshal post: %w", err)
	}

	url := b.ServerURL + "/api/v4/posts"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("mattermost: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.BotToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("mattermost: post message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("mattermost: post message: status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// Still succeeded, just couldn't parse the ID.
		return "", nil
	}
	return result.ID, nil
}

// Notify sends an unsolicited message to channelID. It is used by the
// §7.1 Layer 6 completion-push poller to deliver task-completion alerts
// to a configured Mattermost channel without a prior inbound message.
func (b *Bot) Notify(ctx context.Context, channelID, text string) error {
	if channelID == "" {
		return fmt.Errorf("mattermost: notify: channel id required")
	}
	_, err := b.postMessage(ctx, channelID, text)
	return err
}

// fetchOwnUser queries GET /api/v4/users/me to populate b.userID.
// Called once after WebSocket connection to enable self-message filtering.
func (b *Bot) fetchOwnUser(ctx context.Context) error {
	url := b.ServerURL + "/api/v4/users/me"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("mattermost: build users/me request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.BotToken)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mattermost: fetch users/me: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("mattermost: fetch users/me: %d: %s", resp.StatusCode, string(body))
	}

	var user struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return fmt.Errorf("mattermost: decode users/me: %w", err)
	}
	if user.ID != "" {
		b.Logger.Printf("mattermost: own user ID: %s", user.ID)
	}
	b.userID = user.ID
	return nil
}

// mattermostReply maps gateway.Reply onto the Mattermost REST API.
// Mattermost doesn't support editing messages in place, so Update
// and Final are no-ops after the initial post.
type mattermostReply struct {
	bot       *Bot
	channelID string

	mu        sync.Mutex
	messageID string
}

func (r *mattermostReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
	id, err := r.bot.postMessage(ctx, r.channelID, text)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.messageID = id
	r.mu.Unlock()
	return id, nil
}

func (r *mattermostReply) Update(_ context.Context, _ string, _ string) error {
	// Mattermost doesn't support editing messages in place.
	// Streaming updates are not supported — the full response
	// arrives once via Final.
	return nil
}

func (r *mattermostReply) Final(ctx context.Context, handle string, text string) error {
	// Mattermost doesn't support editing messages in place, so Final
	// posts the complete response as a new message. If the dispatcher
	// sent the full response via PostInitial this produces a duplicate,
	// but in streaming mode where PostInitial sends "⏳ thinking…" this
	// is the only delivery of the actual answer.
	text = gateway.TruncateMessage(text, 16383) // post message limit
	_, err := r.bot.postMessage(ctx, r.channelID, text)
	return err
}

func (r *mattermostReply) Error(ctx context.Context, handle string, err error) error {
	_, postErr := r.bot.postMessage(ctx, r.channelID, "⚠️ "+err.Error())
	return postErr
}

func (r *mattermostReply) StartTyping() (stop func()) {
	// Mattermost doesn't have a native typing indicator for bots.
	return func() {}
}
