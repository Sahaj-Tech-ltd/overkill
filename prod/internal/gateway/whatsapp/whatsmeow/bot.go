// Package whatsmeow — WhatsApp gateway via go.mau.fi/whatsmeow
// (Batch L, unofficial-path backend).
//
// Strict upgrade over the Baileys-Node-sidecar pattern from the
// original plan: Go-native, no Node runtime, no IPC. Same TOS
// posture as Baileys though — WhatsApp's terms allow personal use
// but Meta has banned automated accounts in the past. This backend
// is "personal use only" by design; productizing means using the
// Cloud API path instead.
//
// Pairing happens via the `overkill whatsapp pair` CLI subcommand
// (registered by cmd/overkill). The Bot itself only handles
// pre-paired connections — it loads the existing device from the
// SQLite store and reconnects. A fresh install with no paired
// device returns a clear error pointing at the pair command.
package whatsmeow

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	// third-party library requirement — whatsmeow's sqlstore layer
	// requires SQLite for device key/session storage. This is NOT
	// first-party Overkill storage (which uses PostgreSQL exclusively,
	// per AGENTS.md). The sole consumer is the whatsmeow library itself.
	_ "modernc.org/sqlite"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

var _ gateway.Notifier = (*Bot)(nil)

// Bot implements gateway.Channel for WhatsApp via whatsmeow.
type Bot struct {
	// StorePath is the SQLite file holding the device's E2E keys
	// and session state. Created by the pair command; the Bot fails
	// fast if it doesn't exist (no implicit pairing — that would
	// surprise users with a QR code on every fresh install).
	StorePath string
	// AllowedFrom optionally restricts which sender JIDs the bot
	// will respond to (E.164 form, no leading +, e.g. "14155551234").
	// Empty = any.
	AllowedFrom map[string]bool
	Dispatcher  *gateway.Dispatcher
	Logger      *log.Logger

	// AlertSink, when set, surfaces gateway-level events (LoggedOut,
	// pairing required, etc.) into the central alert stream so they
	// reach the user instead of vanishing into a log file. Optional.
	AlertSink AlertSink
	// SessionID is stamped on alerts so the receiving UI can scope
	// notifications to the right session.
	SessionID string

	mu     sync.Mutex
	client *whatsmeow.Client

	// disconnectCh is signaled by the Disconnected event handler to
	// trigger reconnection in connectWithBackoff. Without this, a
	// network drop would leave the bot silently dead until ctx cancels.
	disconnectCh chan struct{}

	// containerMu protects the SQLite store container. Closed and
	// replaced on each reconnect to prevent file-descriptor exhaustion.
	containerMu sync.Mutex
	container   *sqlstore.Container

	// Health / reconnect state
	healthMu       sync.Mutex
	connected      bool
	lastConnected  time.Time
	backoffAttempt int

	// runCtx carries the Run context for dispatching, so in-flight
	// handlers survive only as long as the bot is alive.
	runCtx   context.Context
	runCtxMu sync.Mutex
}

// AlertSink is the minimal interface the bot needs to surface
// gateway-level alerts. Implemented by journal.AlertStore.
type AlertSink interface {
	Create(alertType, message, sessionID string) error
}

// NewBot returns a Bot bound to a SQLite store path. The store must
// exist (created by the pair flow) — Run returns an error otherwise.
func NewBot(storePath string, allowedFrom []string, d *gateway.Dispatcher) *Bot {
	allow := make(map[string]bool, len(allowedFrom))
	for _, p := range allowedFrom {
		allow[strings.TrimPrefix(strings.TrimSpace(p), "+")] = true
	}
	return &Bot{
		StorePath:   storePath,
		AllowedFrom: allow,
		Dispatcher:  d,
		Logger:      log.New(io.Discard, "", 0),
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "whatsapp-whatsmeow" }

// --- health and reconnect ---

// backoff returns the current backoff duration and advances the
// attempt counter. Caps at 30 seconds.
func (b *Bot) backoff() time.Duration {
	b.healthMu.Lock()
	attempt := b.backoffAttempt
	b.backoffAttempt++
	b.healthMu.Unlock()
	// Cap attempt to prevent integer overflow in math.Pow.
	if attempt > 30 {
		attempt = 30
	}
	d := time.Duration(math.Pow(2, float64(attempt))) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// Healthy reports whether the whatsmeow client is connected and has
// been connected (with recent ping/pong traffic) within 60 seconds.
func (b *Bot) Healthy() bool {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()
	if !b.connected {
		return false
	}
	if b.lastConnected.IsZero() {
		return false
	}
	return time.Since(b.lastConnected) < 60*time.Second
}

// Reconnect disconnects the current client and triggers a fresh
// connection with the configured store.
func (b *Bot) Reconnect(ctx context.Context) error {
	b.mu.Lock()
	b.healthMu.Lock()
	b.connected = false
	client := b.client
	b.healthMu.Unlock()
	b.mu.Unlock()

	if client != nil {
		client.Disconnect()
	}

	// Re-open the store and connect.
	return b.connectWithBackoff(ctx)
}

// Notify sends an unsolicited WhatsApp message to the given JID.
// Used by the §7.1 Layer 6 completion-push poller. Returns an
// error when the whatsmeow client isn't connected (Run hasn't
// finished pairing, or the session has dropped).
func (b *Bot) Notify(ctx context.Context, jidStr, text string) error {
	if jidStr == "" {
		return fmt.Errorf("whatsmeow: notify: jid required")
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return fmt.Errorf("whatsmeow: notify: parse jid %q: %w", jidStr, err)
	}
	b.mu.Lock()
	client := b.client
	b.mu.Unlock()
	if client == nil {
		return fmt.Errorf("whatsmeow: notify: client not connected")
	}
	if _, err := client.SendMessage(ctx, jid, &waE2E.Message{
		Conversation: proto.String(text),
	}); err != nil {
		return fmt.Errorf("whatsmeow: notify send: %w", err)
	}
	return nil
}

// Run loads the paired device, connects, and blocks until ctx is
// cancelled. On disconnect the bot reconnects with exponential backoff
// (1s, 2s, 4s, 8s, 16s, 30s cap). Returns ctx.Err() on cancel.
func (b *Bot) Run(ctx context.Context) error {
	b.runCtxMu.Lock()
	b.runCtx = ctx
	b.runCtxMu.Unlock()

	if b.StorePath == "" {
		return fmt.Errorf("whatsmeow: store_path is required — no database file specified for WhatsApp device storage. Provide a path like ~/.overkill/whatsapp/whatsmeow.db")
	}
	if _, err := os.Stat(b.StorePath); os.IsNotExist(err) {
		return fmt.Errorf("whatsmeow: no paired device found at %q — this WhatsApp gateway needs to be paired first. Run `overkill whatsapp pair` to scan the QR code with your phone's WhatsApp (Settings → Linked Devices). The pairing will create the device store at this path", b.StorePath)
	}

	return b.connectWithBackoff(ctx)
}

// connectWithBackoff opens the store, connects the client, and
// reconnects with exponential backoff on any disconnection or
// connection failure. The Disconnected event handler signals
// disconnectCh to break out of the wait and trigger a reconnect.
func (b *Bot) connectWithBackoff(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Close previous container before opening a new one to
		// prevent file-descriptor exhaustion across reconnects.
		b.containerMu.Lock()
		if b.container != nil {
			b.container.Close()
			b.container = nil
		}
		b.containerMu.Unlock()

		client, container, err := openClient(ctx, b.StorePath)
		if err != nil {
			b.Logger.Printf("whatsmeow: open store: %v", err)
			delay := b.backoff()
			b.Logger.Printf("whatsmeow: backoff %v before retry", delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		b.containerMu.Lock()
		b.container = container
		b.containerMu.Unlock()

		if client.Store.ID == nil {
			// Store exists but isn't paired — same UX as missing file.
			return fmt.Errorf("whatsmeow: store at %q exists but has no paired device — run `overkill whatsapp pair` to scan the QR code and link your phone (Settings → Linked Devices → Link a Device)", b.StorePath)
		}

		b.mu.Lock()
		b.client = client
		b.mu.Unlock()

		// Fresh disconnect channel for this connection attempt.
		b.disconnectCh = make(chan struct{}, 1)

		client.AddEventHandler(b.handleEvent)

		if err := client.Connect(); err != nil {
			b.Logger.Printf("whatsmeow: connect: %v", err)
			b.healthMu.Lock()
			b.connected = false
			b.healthMu.Unlock()
			client.Disconnect()
			delay := b.backoff()
			b.Logger.Printf("whatsmeow: backoff %v before reconnect", delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		// Connected successfully — reset backoff.
		b.healthMu.Lock()
		b.connected = true
		b.lastConnected = time.Now()
		b.backoffAttempt = 0
		b.healthMu.Unlock()
		b.Logger.Printf("whatsmeow: connected as %s", client.Store.ID)

		// Wait for either context cancellation or a Disconnected event.
		select {
		case <-ctx.Done():
			client.Disconnect()
			b.healthMu.Lock()
			b.connected = false
			b.healthMu.Unlock()
			return ctx.Err()
		case <-b.disconnectCh:
			client.Disconnect()
			b.healthMu.Lock()
			b.connected = false
			b.healthMu.Unlock()
			// Loop back to reconnect.
		}
	}
}

// OpenClientForPair is the exported entry point the pair command
// uses. Same store-open logic as the Bot's connect path; exported
// so the QR-pairing CLI doesn't have to dup the boilerplate.
func OpenClientForPair(ctx context.Context, path string) (*whatsmeow.Client, error) {
	client, _, err := openClient(ctx, path)
	return client, err
}

// openClient creates a whatsmeow Client backed by SQLite at path.
// Returns the sqlstore container alongside the client so the caller
// can close it when creating a new connection (prevents FD exhaustion).
// Internal helper shared by connectWithBackoff and OpenClientForPair.
func openClient(ctx context.Context, path string) (*whatsmeow.Client, *sqlstore.Container, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, nil, fmt.Errorf("whatsmeow: mkdir store dir: %w", err)
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)"
	dbLog := waLog.Noop
	// The whatsmeow library requires SQLite for device-key/session
	// storage (third-party requirement, not first-party Overkill
	// storage — which uses PostgreSQL per AGENTS.md).
	container, err := sqlstore.New(ctx, "sqlite", dsn, dbLog)
	if err != nil {
		return nil, nil, fmt.Errorf("whatsmeow: open store: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("whatsmeow: load device: %w", err)
	}
	return whatsmeow.NewClient(device, waLog.Noop), container, nil
}

// handleEvent dispatches whatsmeow events to the right handler. We
// only care about Message events; the rest log at debug level.
// Connected/Disconnected events update the health state.
func (b *Bot) handleEvent(ev any) {
	switch e := ev.(type) {
	case *events.Message:
		b.handleMessage(e)
	case *events.Connected:
		b.healthMu.Lock()
		b.connected = true
		b.lastConnected = time.Now()
		b.healthMu.Unlock()
		b.Logger.Printf("whatsmeow: connected (event)")
	case *events.Disconnected:
		b.healthMu.Lock()
		b.connected = false
		b.healthMu.Unlock()
		b.Logger.Printf("whatsmeow: disconnected; reconnecting with backoff")
		// Signal connectWithBackoff to break out of its wait and reconnect.
		select {
		case b.disconnectCh <- struct{}{}:
		default:
		}
	case *events.LoggedOut:
		// Log AND surface a structured alert so a daemon-mode user
		// actually finds out the bot stopped working. Whatsmeow's
		// auto-reconnect doesn't help after LoggedOut — the device
		// store is wiped and we need a fresh QR-pair to recover.
		b.Logger.Printf("whatsmeow: logged out by phone — the device was unlinked from the phone's WhatsApp. Run `overkill whatsapp pair` to re-link via QR code (Settings → Linked Devices → Link a Device)")
		if b.AlertSink != nil {
			func() {
				defer func() { _ = recover() }()
				_ = b.AlertSink.Create(
					"gateway_logged_out",
					"WhatsApp gateway logged out — the device was unlinked from the phone. Run `overkill whatsapp pair` to re-link via QR code.",
					b.SessionID,
				)
			}()
		}
	}
}

// handleMessage converts a WhatsApp message into gateway.Inbound and
// hands it to the dispatcher.
func (b *Bot) handleMessage(e *events.Message) {
	if e.Info.IsFromMe {
		return
	}

	from := e.Info.Sender.User
	if len(b.AllowedFrom) > 0 && !b.AllowedFrom[from] {
		b.Logger.Printf("whatsmeow: drop disallowed sender %s", from)
		return
	}

	text := ""
	if e.Message.GetConversation() != "" {
		text = e.Message.GetConversation()
	} else if ext := e.Message.GetExtendedTextMessage(); ext != nil {
		text = ext.GetText()
	}

	in := gateway.Inbound{
		Channel:  "whatsapp",
		ChatKey:  from,
		From:     from,
		Text:     strings.TrimSpace(text),
		IsDirect: !e.Info.IsGroup,
	}

	// Image: whatsmeow gives us the encrypted blob, decrypts via the
	// client.Download method. Captions live on the image message.
	if img := e.Message.GetImageMessage(); img != nil {
		b.mu.Lock()
		client := b.client
		b.mu.Unlock()
		if client != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			bytes, err := client.Download(ctx, img)
			cancel()
			if err != nil {
				b.Logger.Printf("whatsmeow: download image: %v", err)
			} else {
				mime := img.GetMimetype()
				if mime == "" {
					mime = vision.MIMEFromBytes(bytes)
				}
				in.Images = append(in.Images, gateway.InboundImage{Bytes: bytes, Mime: mime})
				if in.Text == "" {
					in.Text = strings.TrimSpace(img.GetCaption())
				}
			}
		}
	}

	if in.Text == "" && len(in.Images) == 0 {
		return
	}

	reply := &whatsmeowReply{bot: b, to: e.Info.Sender}
	b.runCtxMu.Lock()
	dispatchCtx := b.runCtx
	b.runCtxMu.Unlock()
	if dispatchCtx == nil {
		dispatchCtx = context.Background()
	}
	if dispatchCtx.Err() != nil {
		log.Printf("whatsapp: run context cancelled, dropping message")
		return
	}
	go b.Dispatcher.Handle(dispatchCtx, in, reply)
}

// whatsmeowReply implements gateway.Reply. WhatsApp via whatsmeow
// also has no edit-in-place for text messages (the protocol supports
// editing but only within 15min and most clients show "edited"
// badges that clutter the conversation). We use the same one-message-
// per-final pattern as the Cloud API backend.
type whatsmeowReply struct {
	bot *Bot
	to  types.JID
}

func (r *whatsmeowReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
	if text == "" {
		text = "⏳ thinking…"
	}
	return r.send(ctx, text)
}

func (r *whatsmeowReply) Update(_ context.Context, _, _ string) error {
	return nil // see comment on whatsmeowReply
}

func (r *whatsmeowReply) Final(ctx context.Context, _, text string) error {
	text = gateway.TruncateMessage(text, 4096) // same as Telegram via whatsmeow
	_, err := r.send(ctx, text)
	return err
}

func (r *whatsmeowReply) Error(ctx context.Context, _ string, err error) error {
	_, sendErr := r.send(ctx, "⚠️ "+err.Error())
	return sendErr
}

func (r *whatsmeowReply) StartTyping() (stop func()) { return func() {} }

func (r *whatsmeowReply) send(ctx context.Context, text string) (string, error) {
	r.bot.mu.Lock()
	client := r.bot.client
	r.bot.mu.Unlock()
	if client == nil {
		return "", fmt.Errorf("whatsmeow: client not connected")
	}
	resp, err := client.SendMessage(ctx, r.to, &waE2E.Message{
		Conversation: proto.String(text),
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}
