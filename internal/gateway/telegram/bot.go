package telegram

import (
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
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

	offset int
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
// hiccups back off; "fatal" errors surface to the caller.
func (b *Bot) Run(ctx context.Context) error {
	go b.registerCommands(ctx)

	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		updates, err := b.Client.GetUpdates(ctx, b.offset, b.PollEvery)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			b.Logger.Printf("telegram: poll: %v (retry in %s)", err, backoff)
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
		backoff = time.Second
		for _, u := range updates {
			if u.UpdateID >= b.offset {
				b.offset = u.UpdateID + 1
			}
			b.handle(ctx, u)
		}
	}
}

func (b *Bot) handle(ctx context.Context, u Update) {
	if u.Message == nil {
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
	go b.Dispatcher.Handle(ctx, in, reply)
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

// chunkAtRune splits at a rune boundary near max, preferring the last
// newline within the window so we don't cut mid-paragraph.
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
