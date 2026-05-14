package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/telegram"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// startCompletionNotifyPoller spins up a goroutine that reads
// AlertTaskCompleted records out of the on-disk AlertStore and pushes
// each one to configured channels' notify targets. Returns a cancel
// function that drains the in-flight poll on shutdown.
//
// Why polling instead of RPC: the daemon and gateway are separate
// processes. The daemon writes alerts to a shared file (AlertStore);
// the gateway reads them. No socket protocol, no version coupling.
// 5-second cadence is fast enough for human-noticed latency while
// being cheap on disk.
//
// On send success, the alert is Acknowledge'd so the next poll
// doesn't re-deliver. On send failure we leave it pending; next
// poll retries until ack succeeds.
func startCompletionNotifyPoller(ctx context.Context, cfg *config.Config, logger *log.Logger) func() {
	home, err := os.UserHomeDir()
	if err != nil {
		logger.Printf("notify-poller: skip (no HOME): %v", err)
		return func() {}
	}
	store := journal.NewAlertStore(filepath.Join(home, ".overkill", "alerts"))
	if err := store.Load(); err != nil {
		logger.Printf("notify-poller: alert store load: %v", err)
		return func() {}
	}

	senders := buildNotifySenders(cfg, logger)
	if len(senders) == 0 {
		// No channel has a notify target configured. Pollster is a
		// no-op — the alerts still accumulate for whoever else reads
		// the store (TUI boot reader, journal_search).
		return func() {}
	}

	pollCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-t.C:
				drainCompletionAlerts(pollCtx, store, senders, logger)
			}
		}
	}()
	logger.Printf("notify-poller: armed (%d destination(s), 5s cadence)", len(senders))
	return func() {
		cancel()
		wg.Wait()
	}
}

// notifySender is one configured push target. Each enabled channel
// with a notify_chat target contributes one entry. We fan out the
// alert to all of them so a single user with multiple devices gets
// it everywhere.
type notifySender struct {
	name string
	send func(ctx context.Context, text string) error
}

func buildNotifySenders(cfg *config.Config, logger *log.Logger) []notifySender {
	var out []notifySender
	if cfg == nil {
		return out
	}

	if t := cfg.Gateways.Telegram; t.NotifyChatID != 0 {
		token := t.BotToken
		if token == "" {
			token = os.Getenv("TELEGRAM_BOT_TOKEN")
		}
		if token != "" {
			client := telegram.New(token)
			chatID := t.NotifyChatID
			out = append(out, notifySender{
				name: "telegram",
				send: func(ctx context.Context, text string) error {
					_, err := client.SendMessage(ctx, chatID, text)
					return err
				},
			})
			logger.Printf("notify-poller: telegram chat %d", chatID)
		} else {
			logger.Printf("notify-poller: telegram notify_chat_id set but no bot token; skipping")
		}
	}

	// Discord notify: ChannelMessageSend doesn't need a live session
	// reference — discordgo gives us the channel ID and we send via
	// REST. But constructing a discordgo.Session here would duplicate
	// what bot.go owns. Defer to a future iteration; the alert still
	// lands in the store and the TUI boot reader picks it up.
	if dc := cfg.Gateways.Discord; dc.NotifyChannelID != "" {
		logger.Printf("notify-poller: discord notify wired (TODO: push via REST without session sharing)")
	}

	// WhatsApp notify: similar story. The whatsmeow client maintains
	// state inside the bot; reusing it for push requires sharing the
	// instance. Future work.
	if wa := cfg.Gateways.WhatsApp; wa.NotifyJID != "" {
		_ = types.NewJID // keep the import referenced for the future wiring
		logger.Printf("notify-poller: whatsapp notify wired (TODO: push via shared whatsmeow client)")
	}

	return out
}

// drainCompletionAlerts reads pending alerts, pushes the
// AlertTaskCompleted ones, and acknowledges on success. Other alert
// types are left alone — the TUI boot reader is their consumer.
func drainCompletionAlerts(ctx context.Context, store *journal.AlertStore, senders []notifySender, logger *log.Logger) {
	pending := store.Pending()
	for _, a := range pending {
		if a.Type != journal.AlertTaskCompleted {
			continue
		}
		anySent := false
		for _, s := range senders {
			sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := s.send(sendCtx, a.Message)
			cancel()
			if err != nil {
				logger.Printf("notify-poller: %s send: %v", s.name, err)
				continue
			}
			anySent = true
		}
		if anySent {
			if err := store.Acknowledge(a.ID); err != nil {
				logger.Printf("notify-poller: ack %s: %v", a.ID, err)
			}
		}
	}
}
