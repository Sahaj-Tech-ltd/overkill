// Package discord — Discord gateway via discordgo (Batch H).
//
// Same shape as the Telegram bot: convert incoming events to
// gateway.Inbound, hand to the dispatcher, format replies through
// the gateway.Reply interface. Differences from Telegram:
//
//   - Connection is a WebSocket session, not long polling. Open()
//     opens the gateway; Close() drops it. The bot's Run method
//     wraps both around context lifecycle.
//   - Discord supports message edits (rich embed updates too) so
//     streaming Just Works the way it does in Telegram.
//   - In guild channels we ignore messages that don't @mention the
//     bot — `RequireMention` defaults true. DMs always work.
//   - Allow-list is two-axis: guild ids AND channel ids. Empty lists
//     mean "any".
package discord

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

// Bot implements gateway.Channel for Discord. One Bot per token.
type Bot struct {
	Token           string
	Dispatcher      *gateway.Dispatcher
	AllowedGuilds   map[string]bool
	AllowedChannels map[string]bool
	RequireMention  bool
	Logger          *log.Logger

	session *discordgo.Session
	selfID  string

	mu     sync.Mutex
	closed bool

	// Health / reconnect state
	healthMu         sync.Mutex
	connected        bool
	lastHeartbeatAck time.Time
	backoffAttempt   int

	// Per-channel edit rate limits (5 edits per 5s per channel).
	// Discord enforces this server-side; we track it client-side
	// so we can throttle before hitting HTTP 429.
	editRLMu       sync.Mutex
	editRateLimits map[string]*editRL
}

// NewBot returns a Bot wired to a token + dispatcher. Allow-list
// slices may be empty to permit any guild/channel.
func NewBot(token string, d *gateway.Dispatcher, allowedGuilds, allowedChannels []string, requireMention bool) *Bot {
	gset := make(map[string]bool, len(allowedGuilds))
	for _, g := range allowedGuilds {
		gset[g] = true
	}
	cset := make(map[string]bool, len(allowedChannels))
	for _, c := range allowedChannels {
		cset[c] = true
	}
	return &Bot{
		Token:           token,
		Dispatcher:      d,
		AllowedGuilds:   gset,
		AllowedChannels: cset,
		RequireMention:  requireMention,
		Logger:          log.New(io.Discard, "", 0),
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "discord" }

// --- edit rate limiter (5 edits per 5s per channel) ---

// editRL is a simple sliding-window rate limiter for Discord's
// ChannelMessageEdit endpoint (5 edits / 5 seconds per channel).
type editRL struct {
	timestamps []time.Time
}

func (r *editRL) allow(now time.Time) bool {
	// Prune old entries beyond the 5-second window.
	cutoff := now.Add(-5 * time.Second)
	i := 0
	for ; i < len(r.timestamps); i++ {
		if r.timestamps[i].After(cutoff) {
			break
		}
	}
	r.timestamps = r.timestamps[i:]
	if len(r.timestamps) < 5 {
		r.timestamps = append(r.timestamps, now)
		return true
	}
	return false
}

func (b *Bot) editCheck(channelID string) bool {
	b.editRLMu.Lock()
	defer b.editRLMu.Unlock()
	if b.editRateLimits == nil {
		b.editRateLimits = make(map[string]*editRL)
	}
	rl, ok := b.editRateLimits[channelID]
	if !ok {
		rl = &editRL{}
		b.editRateLimits[channelID] = rl
	}
	return rl.allow(time.Now())
}

// --- health and reconnect ---

// backoff returns the current backoff duration and advances the
// attempt counter. Caps at 30 seconds. The caller must reset the
// counter to 0 on a successful connection.
func (b *Bot) backoff() time.Duration {
	b.healthMu.Lock()
	attempt := b.backoffAttempt
	b.backoffAttempt++
	b.healthMu.Unlock()
	d := time.Duration(math.Pow(2, float64(attempt))) * time.Second
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// Healthy reports whether the gateway is connected and received a
// heartbeat acknowledgment within the last 60 seconds.
func (b *Bot) Healthy() bool {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()
	if !b.connected {
		return false
	}
	return time.Since(b.lastHeartbeatAck) < 60*time.Second
}

// Reconnect closes the current session and opens a new one with
// exponential backoff.
func (b *Bot) Reconnect(ctx context.Context) error {
	b.mu.Lock()
	b.healthMu.Lock()
	b.connected = false
	sess := b.session
	b.healthMu.Unlock()
	b.mu.Unlock()

	if sess != nil {
		_ = sess.Close()
	}

	return b.connectLoop(ctx, sess)
}

// Notify sends an unsolicited message to channelID. Returns an
// error when the bot's gateway session isn't open yet (Run hasn't
// established it, or has already shut down). Used by the §7.1
// Layer 6 completion-push poller to deliver task alerts to a
// configured channel without a prior inbound message.
func (b *Bot) Notify(ctx context.Context, channelID, text string) error {
	if channelID == "" {
		return fmt.Errorf("discord: notify: channel id required")
	}
	b.mu.Lock()
	sess := b.session
	closed := b.closed
	b.mu.Unlock()
	if sess == nil || closed {
		return fmt.Errorf("discord: notify: session not open")
	}
	_, err := sess.ChannelMessageSend(channelID, text)
	if err != nil {
		return fmt.Errorf("discord: notify: %w", err)
	}
	return nil
}

// Run opens the Discord gateway connection and blocks until ctx is
// cancelled. On disconnect the bot reconnects with exponential backoff
// (1s, 2s, 4s, 8s, 16s, 30s cap). Returns ctx.Err() on cancel.
func (b *Bot) Run(ctx context.Context) error {
	sess, err := discordgo.New("Bot " + b.Token)
	if err != nil {
		return fmt.Errorf("discord: new session: %w", err)
	}
	b.mu.Lock()
	b.session = sess
	b.mu.Unlock()

	// Intents: we need MessageContent (so we can read message text)
	// and the message events. Guild messages + DM messages.
	sess.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent

	sess.AddHandler(b.onMessage)
	sess.AddHandler(b.onReady)

	return b.connectLoop(ctx, sess)
}

// connectLoop connects with exponential backoff, waits for
// disconnect, then reconnects. It respects ctx cancellation.
func (b *Bot) connectLoop(ctx context.Context, sess *discordgo.Session) error {
	for {
		// Check ctx before attempting.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Create a fresh session if the old one was closed.
		if sess == nil {
			var err error
			sess, err = discordgo.New("Bot " + b.Token)
			if err != nil {
				b.Logger.Printf("discord: new session: %v", err)
				delay := b.backoff()
				b.Logger.Printf("discord: backoff %v before retry", delay)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
				}
				continue
			}
			sess.Identify.Intents = discordgo.IntentsGuildMessages |
				discordgo.IntentsDirectMessages |
				discordgo.IntentsMessageContent
			sess.AddHandler(b.onMessage)
			sess.AddHandler(b.onReady)
			b.mu.Lock()
			b.session = sess
			b.mu.Unlock()
		}

		if err := sess.Open(); err != nil {
			b.Logger.Printf("discord: open gateway: %v", err)
			// Mark disconnected.
			b.healthMu.Lock()
			b.connected = false
			b.healthMu.Unlock()
			// Close the dead session so we create a new one.
			_ = sess.Close()
			sess = nil
			delay := b.backoff()
			b.Logger.Printf("discord: backoff %v before reconnect", delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			continue
		}

		// Open() blocks until the websocket closes (disconnect).
		// When we get here, the connection dropped.
		b.healthMu.Lock()
		b.connected = false
		b.healthMu.Unlock()
		sess = nil // force fresh session next loop

		delay := b.backoff()
		b.Logger.Printf("discord: disconnected, backoff %v before reconnect", delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// onReady captures the bot's own user ID so onMessage can recognize
// mentions of itself. discordgo fires Ready exactly once on each
// successful gateway handshake. We reset the backoff counter and mark
// the gateway as connected for health checks.
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	b.mu.Lock()
	b.selfID = r.User.ID
	b.mu.Unlock()
	b.healthMu.Lock()
	b.connected = true
	b.lastHeartbeatAck = time.Now()
	b.backoffAttempt = 0
	b.healthMu.Unlock()
	b.Logger.Printf("discord: ready as %s (%s)", r.User.Username, r.User.ID)
}

// onMessage is discordgo's MessageCreate handler. We filter aggressively
// (bot's own messages, disallowed guilds/channels, mention-required-but-
// missing) before constructing a gateway.Inbound and handing to the
// dispatcher. Also bumps the health-check timestamp since any event
// means the connection is alive.
func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Bump health timestamp — any event proves the WS is alive.
	b.healthMu.Lock()
	b.lastHeartbeatAck = time.Now()
	b.healthMu.Unlock()

	// Skip our own messages — discordgo doesn't auto-filter, and
	// without this we'd loop on every reply we post.
	b.mu.Lock()
	selfID := b.selfID
	b.mu.Unlock()
	if m.Author == nil || m.Author.ID == selfID || m.Author.Bot {
		return
	}

	// Allow-list check. Discord guild messages have a non-empty
	// GuildID; DMs have GuildID == "". Empty AllowedGuilds means any
	// guild is fine; same for channels.
	if m.GuildID != "" && len(b.AllowedGuilds) > 0 && !b.AllowedGuilds[m.GuildID] {
		return
	}
	if len(b.AllowedChannels) > 0 && !b.AllowedChannels[m.ChannelID] {
		return
	}

	isDM := m.GuildID == ""
	mentioned := b.mentionsSelf(m, selfID)

	// In guild channels, require @mention unless explicitly opt-out.
	// DMs always proceed.
	if !isDM && b.RequireMention && !mentioned {
		return
	}

	text := strings.TrimSpace(stripMention(m.Content, selfID))
	if text == "" && len(m.Attachments) == 0 {
		return
	}

	from := ""
	if m.Author != nil {
		from = m.Author.Username
	}

	in := gateway.Inbound{
		Channel:  "discord",
		ChatKey:  m.ChannelID, // one chat per channel/DM
		From:     from,
		Text:     text,
		IsDirect: isDM,
	}

	// Fetch image attachments. Discord serves attachments via CDN
	// URL; we download synchronously inside a short ctx so a slow CDN
	// can't stall the gateway.
	for _, att := range m.Attachments {
		if !strings.HasPrefix(strings.ToLower(att.ContentType), "image/") {
			continue
		}
		img, err := b.fetchImage(att.URL)
		if err != nil {
			b.Logger.Printf("discord: fetch image %s: %v", att.URL, err)
			continue
		}
		in.Images = append(in.Images, img)
	}

	reply := &discordReply{bot: b, session: s, channelID: m.ChannelID}
	// Best-effort: each message gets its own goroutine so a slow
	// dispatcher call doesn't head-of-line block the gateway. The
	// session router serializes per chat key.
	go b.Dispatcher.Handle(context.Background(), in, reply)
}

// mentionsSelf reports whether the message's Mentions array contains
// our own user ID. We don't pattern-match the raw text because
// discord renders mentions as <@123> snowflakes — the structured
// Mentions array is the truth.
func (b *Bot) mentionsSelf(m *discordgo.MessageCreate, selfID string) bool {
	if selfID == "" {
		return false
	}
	for _, u := range m.Mentions {
		if u != nil && u.ID == selfID {
			return true
		}
	}
	return false
}

// stripMention removes the leading <@selfID> mention snippet from the
// message body so the agent doesn't see "@overkillbot what's up" but
// just "what's up". Multiple discord mention forms exist (<@ID> and
// <@!ID> for nick mentions); both get stripped.
func stripMention(content, selfID string) string {
	if selfID == "" {
		return content
	}
	for _, form := range []string{"<@" + selfID + ">", "<@!" + selfID + ">"} {
		content = strings.ReplaceAll(content, form, "")
	}
	return content
}

// fetchImage downloads a Discord attachment into bytes. Capped at 8MB
// (Discord's default upload limit for non-Nitro accounts) so a
// pathological URL can't OOM us.
func (b *Bot) fetchImage(url string) (gateway.InboundImage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return gateway.InboundImage{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return gateway.InboundImage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return gateway.InboundImage{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	const maxBytes = 8 * 1024 * 1024
	bytes, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return gateway.InboundImage{}, err
	}
	return gateway.InboundImage{Bytes: bytes, Mime: vision.MIMEFromBytes(bytes)}, nil
}

// discordReply maps gateway.Reply onto discordgo session methods.
// Streaming uses ChannelMessageEdit — Discord's edit API rate-limits
// at ~5/5s per channel, which is finer-grained than Telegram and
// matches Dispatcher.UpdateEvery defaults (750ms).
type discordReply struct {
	bot       *Bot
	session   *discordgo.Session
	channelID string

	mu        sync.Mutex
	messageID string
}

func (r *discordReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
	msg, err := r.session.ChannelMessageSend(r.channelID, text)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.messageID = msg.ID
	r.mu.Unlock()
	return msg.ID, nil
}

func (r *discordReply) Update(_ context.Context, handle, text string) error {
	if text == "" {
		text = "⏳ thinking…"
	}
	// Respect Discord's 5-edits-per-5-seconds per-channel rate limit.
	// If the limiter says no, we skip this update — the dispatcher
	// will deliver the next chunk (or Final) shortly.
	if r.bot != nil && !r.bot.editCheck(r.channelID) {
		return nil
	}
	_, err := r.session.ChannelMessageEdit(r.channelID, handle, text)
	return err
}

func (r *discordReply) Final(ctx context.Context, handle, text string) error {
	return r.Update(ctx, handle, text)
}

func (r *discordReply) Error(ctx context.Context, handle string, err error) error {
	return r.Update(ctx, handle, "⚠️ "+err.Error())
}

func (r *discordReply) StartTyping() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.session.ChannelTyping(r.channelID)
		ticker := time.NewTicker(8 * time.Second) // discord typing expires after 10s
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.session.ChannelTyping(r.channelID)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
