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
	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

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
// cancelled. Disconnect is automatic on ctx cancel.
func (b *Bot) Run(ctx context.Context) error {
	if b.StorePath == "" {
		return fmt.Errorf("whatsmeow: store_path required")
	}
	if _, err := os.Stat(b.StorePath); os.IsNotExist(err) {
		return fmt.Errorf("whatsmeow: no paired device at %s — run `overkill whatsapp pair` first", b.StorePath)
	}

	client, err := openClient(ctx, b.StorePath)
	if err != nil {
		return err
	}
	if client.Store.ID == nil {
		// Store exists but isn't paired — same UX as missing file.
		return fmt.Errorf("whatsmeow: store at %s has no paired device — run `overkill whatsapp pair`", b.StorePath)
	}

	b.mu.Lock()
	b.client = client
	b.mu.Unlock()

	client.AddEventHandler(b.handleEvent)

	if err := client.Connect(); err != nil {
		return fmt.Errorf("whatsmeow: connect: %w", err)
	}
	b.Logger.Printf("whatsmeow: connected as %s", client.Store.ID)

	<-ctx.Done()
	client.Disconnect()
	return ctx.Err()
}

// OpenClientForPair is the exported entry point the pair command
// uses. Same store-open logic as the Bot's connect path; exported
// so the QR-pairing CLI doesn't have to dup the boilerplate.
func OpenClientForPair(ctx context.Context, path string) (*whatsmeow.Client, error) {
	return openClient(ctx, path)
}

// openClient creates a whatsmeow Client backed by SQLite at path.
// Internal helper shared by Bot.Run and OpenClientForPair.
func openClient(ctx context.Context, path string) (*whatsmeow.Client, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("whatsmeow: mkdir store dir: %w", err)
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)"
	dbLog := waLog.Noop
	container, err := sqlstore.New(ctx, "sqlite", dsn, dbLog)
	if err != nil {
		return nil, fmt.Errorf("whatsmeow: open store: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("whatsmeow: load device: %w", err)
	}
	return whatsmeow.NewClient(device, waLog.Noop), nil
}

// handleEvent dispatches whatsmeow events to the right handler. We
// only care about Message events; the rest log at debug level.
func (b *Bot) handleEvent(ev any) {
	switch e := ev.(type) {
	case *events.Message:
		b.handleMessage(e)
	case *events.Disconnected:
		b.Logger.Printf("whatsmeow: disconnected; whatsmeow will auto-reconnect")
	case *events.LoggedOut:
		// Log AND surface a structured alert so a daemon-mode user
		// actually finds out the bot stopped working. Whatsmeow's
		// auto-reconnect doesn't help after LoggedOut — the device
		// store is wiped and we need a fresh QR-pair to recover.
		// Future work: trigger b.startPairing() automatically and
		// post the new QR via the alert sink instead of expecting
		// the user to notice the log line.
		b.Logger.Printf("whatsmeow: logged out by phone — pair again to recover")
		_ = e // reason field is in e.Reason for future telemetry
		if b.AlertSink != nil {
			func() {
				defer func() { _ = recover() }()
				_ = b.AlertSink.Create(
					"gateway_logged_out",
					"WhatsApp gateway logged out by phone. Run `overkill gateway whatsmeow pair` to re-link.",
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
	go b.Dispatcher.Handle(context.Background(), in, reply)
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
