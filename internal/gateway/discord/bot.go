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
// cancelled. Returns ctx.Err() on cancel, or a wrapped error if the
// initial Open fails.
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

	if err := sess.Open(); err != nil {
		return fmt.Errorf("discord: open gateway: %w", err)
	}
	b.Logger.Printf("discord: gateway open, waiting for events")

	<-ctx.Done()

	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	if err := sess.Close(); err != nil {
		b.Logger.Printf("discord: close: %v", err)
	}
	return ctx.Err()
}

// onReady captures the bot's own user ID so onMessage can recognize
// mentions of itself. discordgo fires Ready exactly once on each
// successful gateway handshake.
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	b.mu.Lock()
	b.selfID = r.User.ID
	b.mu.Unlock()
	b.Logger.Printf("discord: ready as %s (%s)", r.User.Username, r.User.ID)
}

// onMessage is discordgo's MessageCreate handler. We filter aggressively
// (bot's own messages, disallowed guilds/channels, mention-required-but-
// missing) before constructing a gateway.Inbound and handing to the
// dispatcher.
func (b *Bot) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
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

	reply := &discordReply{session: s, channelID: m.ChannelID}
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
