package telegram

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

// Bot implements gateway.Channel for Telegram. One Bot per token; you
// can run multiples concurrently if you want a fleet.
type Bot struct {
	Client     *Client
	Dispatcher *gateway.Dispatcher
	Allowed    map[int64]bool // chat_id allow-list; empty = all
	Logger     *log.Logger
	PollEvery  time.Duration // long-poll timeout; default 60s

	// runCtx carries the Run context for dispatching, so in-flight
	// agent turns are not cancelled when the poll loop restarts.
	runCtx   context.Context
	runCtxMu sync.Mutex

	offset     int
	lastPollOK time.Time
	lastPollMu sync.Mutex
}

// NewBot returns a Bot wired to the given client and dispatcher.
// allowedChats may be empty to allow every chat the bot is invited to.
func NewBot(c *Client, d *gateway.Dispatcher, allowedChats []int64) *Bot {
	allow := map[int64]bool{}
	for _, id := range allowedChats {
		allow[id] = true
	}
	return &Bot{
		Client:     c,
		Dispatcher: d,
		Allowed:    allow,
		Logger:     log.New(io.Discard, "", 0),
		PollEvery:  60 * time.Second,
	}
}

// registeredCommands returns the slash commands for the Telegram bot menu.
// Keep in sync with dispatch.go:handleCommand.
func registeredCommands() []BotCommand {
	return []BotCommand{
		{Command: "help", Description: "Show available commands"},
		{Command: "sessions", Description: "List recent sessions"},
		{Command: "attach", Description: "Bind chat to a session ID"},
		{Command: "follow", Description: "Mirror TUI session"},
		{Command: "unfollow", Description: "Clear follow mode"},
		{Command: "new", Description: "Start a fresh session"},
		{Command: "end", Description: "Clear follow, keep binding"},
		{Command: "bm", Description: "Bookmark current session"},
		{Command: "estop", Description: "Emergency stop all agents"},
	}
}

// registerCommands pushes the slash-command menu to Telegram with retry.
// Run as a goroutine during startup so it never blocks message intake.
func (b *Bot) registerCommands(ctx context.Context) {
	cmds := registeredCommands()
	backoff := 2 * time.Second
	maxBackoff := 5 * time.Minute
	for {
		if err := b.Client.SetMyCommands(ctx, cmds); err != nil {
			b.Logger.Printf("telegram: setMyCommands: %v (retry in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		b.Logger.Printf("telegram: %d commands registered", len(cmds))
		return
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "telegram" }

// Run drives the long-poll loop until ctx is cancelled. Network
// hiccups back off; after maxConsecutiveErrors (10) consecutive
// failures the error surfaces to the caller rather than retrying
// forever (e.g. on a wrong bot token).
func (b *Bot) Run(ctx context.Context) error {
	const maxConsecutiveErrors = 10
	// B003: store run context for dispatcher goroutines.
	b.runCtxMu.Lock()
	b.runCtx = ctx
	b.runCtxMu.Unlock()

	go b.registerCommands(ctx)

	backoff := time.Second
	consecutiveErrors := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		updates, err := b.Client.GetUpdates(ctx, b.offset, b.PollEvery)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			consecutiveErrors++
			b.Logger.Printf("telegram: poll: %v (retry in %s, %d/%d)", err, backoff, consecutiveErrors, maxConsecutiveErrors)
			if consecutiveErrors >= maxConsecutiveErrors {
				return fmt.Errorf("telegram: poll failed %d times, giving up: %w", consecutiveErrors, err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		consecutiveErrors = 0
		b.lastPollMu.Lock()
		b.lastPollOK = time.Now()
		b.lastPollMu.Unlock()
		backoff = time.Second
		for _, u := range updates {
			if u.UpdateID >= b.offset {
				b.offset = u.UpdateID + 1
			}
			b.handle(ctx, u)
		}
	}
}

// Reconnect implements gateway.Reconnecter (B135). The polling loop
// in Run already handles reconnection with backoff; this provides an
// explicit hook for external health monitors to trigger a re-poll
// cycle by cancelling and restarting.
func (b *Bot) Reconnect(ctx context.Context) error {
	// Stub: the poll loop auto-reconnects. Future: signal runCtx
	// to restart cleanly.
	return nil
}

// Healthy implements gateway.HealthChecker — returns true when the
// last successful poll was within 2× PollEvery (default 120s window).
func (b *Bot) Healthy() bool {
	b.lastPollMu.Lock()
	defer b.lastPollMu.Unlock()
	if b.lastPollOK.IsZero() {
		return false // never polled
	}
	window := 2 * b.PollEvery
	if window < 30*time.Second {
		window = 30 * time.Second // floor
	}
	return time.Since(b.lastPollOK) < window
}

// Compile-time interface compliance.
var _ gateway.HealthChecker = (*Bot)(nil)

func (b *Bot) handle(ctx context.Context, u Update) {
	if u.Message == nil {
		// B132: Log unhandled update types so operators can see
		// what Telegram is sending (e.g. callback_query, inline_query).
		// These arrive rarely and logging them costs nothing.
		b.Logger.Printf("telegram: unhandled update type (no message); update_id=%d", u.UpdateID)
		return
	}
	if u.Message.From != nil && u.Message.From.IsBot {
		return
	}
	if u.Message.Text == "" && len(u.Message.Photo) == 0 {
		return
	}
	chatID := u.Message.Chat.ID
	if len(b.Allowed) > 0 && !b.Allowed[chatID] {
		b.Logger.Printf("telegram: skip disallowed chat %d", chatID)
		return
	}
	from := ""
	if u.Message.From != nil {
		from = u.Message.From.Username
		if from == "" {
			from = u.Message.From.FirstName
		}
	}
	text := u.Message.Text
	if text == "" {
		text = u.Message.Caption
	}
	in := gateway.Inbound{
		Channel:  "telegram",
		ChatKey:  strconv.FormatInt(chatID, 10),
		From:     from,
		Text:     text,
		IsDirect: u.Message.Chat.Type == "private",
	}
	if len(u.Message.Photo) > 0 {
		// Largest size is last in the array per the Bot API contract.
		largest := u.Message.Photo[len(u.Message.Photo)-1]
		if img, err := b.fetchPhoto(ctx, largest.FileID); err != nil {
			b.Logger.Printf("telegram: fetch photo: %v", err)
		} else {
			in.Images = append(in.Images, img)
		}
	}
	reply := &telegramReply{client: b.Client, chatID: chatID}
	// B003: use runCtx so dispatcher goroutines don't leak on gateway restart.
	b.runCtxMu.Lock()
	dispatchCtx := b.runCtx
	b.runCtxMu.Unlock()
	if dispatchCtx == nil {
		dispatchCtx = context.Background()
	}
	go b.Dispatcher.Handle(dispatchCtx, in, reply)
}

// fetchPhoto resolves a Telegram file_id to bytes + sniffed MIME. Two
// API hops: getFile to grab the path, then a download from /file/.
func (b *Bot) fetchPhoto(ctx context.Context, fileID string) (gateway.InboundImage, error) {
	dlCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	path, err := b.Client.GetFile(dlCtx, fileID)
	if err != nil {
		return gateway.InboundImage{}, err
	}
	bytes, err := b.Client.DownloadFile(dlCtx, path)
	if err != nil {
		return gateway.InboundImage{}, err
	}
	return gateway.InboundImage{Bytes: bytes, Mime: vision.MIMEFromBytes(bytes)}, nil
}

// telegramReply maps gateway.Reply onto Bot API methods. Telegram lets
// us edit messages in place, so streaming updates Just Work.
type telegramReply struct {
	client *Client
	chatID int64

	mu        sync.Mutex
	messageID int
}

func (r *telegramReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
	id, err := r.client.SendMessage(ctx, r.chatID, text)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.messageID = id
	r.mu.Unlock()
	return strconv.Itoa(id), nil
}

func (r *telegramReply) Update(ctx context.Context, handle, text string) error {
	id, err := strconv.Atoi(handle)
	if err != nil {
		return fmt.Errorf("telegram: bad handle %q: %w", handle, err)
	}
	if text == "" {
		text = "⏳ thinking…"
	}
	return r.client.EditMessageText(ctx, r.chatID, id, text)
}

// telegramMaxLen is the Telegram Bot API hard limit per message
// (https://core.telegram.org/method/messages.sendMessage). Sending a
// longer message returns 400 — the agent's full reply was silently
// lost. We chunk: the initial handle gets the first slice, follow-ups
// are sent as fresh messages so the user sees the whole thing.
const telegramMaxLen = 4096

func (r *telegramReply) Final(ctx context.Context, handle, text string) error {
	// Send the text reply first.
	if err := r.sendFinalText(ctx, handle, text); err != nil {
		return err
	}
	// Auto-detect TTS audio paths and send as voice notes.
	r.sendVoiceNotes(ctx, text)
	return nil
}

// sendFinalText delivers the text content, chunking if over the limit.
func (r *telegramReply) sendFinalText(ctx context.Context, handle, text string) error {
	if len(text) <= telegramMaxLen {
		return r.Update(ctx, handle, text)
	}
	// First chunk replaces the existing thinking-bubble message.
	first, rest := chunkAtRune(text, telegramMaxLen)
	if err := r.Update(ctx, handle, first); err != nil {
		return err
	}
	// Send follow-ups as new messages in the same chat.
	for len(rest) > 0 {
		var part string
		part, rest = chunkAtRune(rest, telegramMaxLen)
		if _, err := r.client.SendMessage(ctx, r.chatID, part); err != nil {
			return err
		}
	}
	return nil
}

// ttsPathRe matches TTS output audio file paths (WAV or MP3) in MEDIA: format.
// Anchored to only match within the MEDIA: prefix so legitimate body text
// containing path-like strings doesn't trigger ffmpeg conversion.
var ttsPathRe = regexp.MustCompile(`MEDIA:/tmp/overkill-tts-[a-f0-9]+\.(mp3|wav)`)

// sendVoiceNotes scans the reply text for TTS audio file paths in MEDIA:
// format, strips the prefix, converts to OGG, and sends as Telegram voice
// notes. Errors are logged but never surfaced — voice delivery is best-effort.
func (r *telegramReply) sendVoiceNotes(ctx context.Context, text string) {
	matches := ttsPathRe.FindAllString(text, -1)
	for _, mediaPath := range matches {
		// Strip MEDIA: prefix to get the real filesystem path.
		audioPath := strings.TrimPrefix(mediaPath, "MEDIA:")
		// Convert to OGG for Telegram voice note playback.
		oggPath, err := convertToOGG(ctx, audioPath)
		if err != nil {
			// Best-effort: skip silently.
			continue
		}
		defer os.Remove(oggPath)

		// Send as a voice note. Short timeout — we already sent the text.
		sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, err = r.client.SendVoice(sendCtx, r.chatID, oggPath)
		cancel()
		if err != nil {
			// Best-effort: skip.
			continue
		}
	}
}

// convertToOGG converts a WAV or MP3 audio file to OGG (libopus 32k) for
// Telegram voice notes. Returns the path to the OGG file. Caller should
// remove it when done. Returns an error if ffmpeg is not installed.
// Security: inputPath MUST be under a known temp directory (validated by
// the ttsPathRe regex in callers). This function adds an additional
// containment check.
func convertToOGG(ctx context.Context, inputPath string) (string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("ffmpeg not installed — required for voice note conversion")
	}

	// Validate inputPath is under a safe directory (e.g. /tmp/overkill-tts-*).
	// This guards against path traversal / command injection via filename.
	if !strings.HasPrefix(filepath.Clean(inputPath), os.TempDir()+string(os.PathSeparator)) {
		return "", fmt.Errorf("tts: input path outside temp dir")
	}

	oggPath := inputPath + ".ogg"

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", inputPath,
		"-c:a", "libopus",
		"-b:a", "32k",
		oggPath,
		"-y",
	)
	// Suppress ffmpeg noise; failures produce an error anyway.
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		_ = os.Remove(oggPath)
		return "", fmt.Errorf("ffmpeg conversion failed: %w", err)
	}

	return oggPath, nil
}

// chunkAtRune splits at a rune boundary near max, preferring the last
// newline within the window so we don't cut mid-paragraph.
//
// Unicode safety: the fallback walks backward through continuation bytes
// (0x80-0xBF, identified by &0xC0==0x80) to find the start of a multi-byte
// UTF-8 rune, ensuring s[:cut] is always valid UTF-8.
func chunkAtRune(s string, max int) (head, tail string) {
	if len(s) <= max {
		return s, ""
	}
	// Walk back from max to find a safe break (preferring newline).
	cut := max
	for cut > 0 && cut > max-200 {
		if s[cut] == '\n' {
			return s[:cut], s[cut+1:]
		}
		cut--
	}
	// Fallback: respect rune boundary at max.
	cut = max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut], s[cut:]
}

// StartTyping implements gateway.Reply. Sends the native Telegram typing
// indicator and refreshes it every 4s until stop() is called.
func (r *telegramReply) StartTyping() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.client.SendChatAction(ctx, r.chatID, "typing")
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.client.SendChatAction(ctx, r.chatID, "typing")
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (r *telegramReply) Error(ctx context.Context, handle string, err error) error {
	return r.Update(ctx, handle, "⚠️ "+err.Error())
}
