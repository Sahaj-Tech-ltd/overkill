package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/discord"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/telegram"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/whatsapp/cloud"
	wameow "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/whatsapp/whatsmeow"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// notifyBots captures references to the bot instances the gateway
// command constructs so the §7.1 Layer 6 completion-push poller can
// deliver alerts through their already-open connections (instead of
// duplicating auth + sessions). Each field is independently nil-
// safe: a channel that's disabled, missing credentials, or hasn't
// finished its Run handshake just skips its send branch.
type notifyBots struct {
	telegramClient   *telegram.Client
	discordBot       *discord.Bot
	whatsmeowBot     *wameow.Bot
	whatsappCloudBot *cloud.Bot
}

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
// poll retries until ack succeeds. Per-channel failures don't
// block fan-out to other channels — a wedged WhatsApp won't keep
// Telegram from delivering.
func startCompletionNotifyPoller(ctx context.Context, cfg *config.Config, logger *log.Logger, bots notifyBots) func() {
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

	senders := buildNotifySenders(cfg, bots, logger)
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

func buildNotifySenders(cfg *config.Config, bots notifyBots, logger *log.Logger) []notifySender {
	var out []notifySender
	if cfg == nil {
		return out
	}

	if t := cfg.Gateways.Telegram; t.NotifyChatID != 0 {
		client := bots.telegramClient
		if client == nil {
			// Fall back to constructing a one-shot client from the
			// configured token. Useful when the user runs gateway
			// without telegram in [gateways] but still wants push.
			token := t.BotToken
			if token == "" {
				token = os.Getenv("TELEGRAM_BOT_TOKEN")
			}
			if token != "" {
				client = telegram.New(token)
			}
		}
		if client != nil {
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

	if dc := cfg.Gateways.Discord; dc.NotifyChannelID != "" {
		if bots.discordBot == nil {
			logger.Printf("notify-poller: discord notify_channel_id set but discord bot not in this hub; skipping")
		} else {
			bot := bots.discordBot
			channelID := dc.NotifyChannelID
			out = append(out, notifySender{
				name: "discord",
				send: func(ctx context.Context, text string) error {
					return bot.Notify(ctx, channelID, text)
				},
			})
			logger.Printf("notify-poller: discord channel %s", channelID)
		}
	}

	if wa := cfg.Gateways.WhatsApp; wa.NotifyJID != "" {
		switch {
		case bots.whatsmeowBot != nil:
			bot := bots.whatsmeowBot
			jid := wa.NotifyJID
			out = append(out, notifySender{
				name: "whatsapp/whatsmeow",
				send: func(ctx context.Context, text string) error {
					return bot.Notify(ctx, jid, text)
				},
			})
			logger.Printf("notify-poller: whatsapp/whatsmeow jid %s", jid)
		case bots.whatsappCloudBot != nil:
			bot := bots.whatsappCloudBot
			jid := wa.NotifyJID
			out = append(out, notifySender{
				name: "whatsapp/cloud",
				send: func(ctx context.Context, text string) error {
					return bot.Notify(ctx, jid, text)
				},
			})
			logger.Printf("notify-poller: whatsapp/cloud to %s", jid)
		default:
			logger.Printf("notify-poller: whatsapp notify_jid set but no whatsapp bot in this hub; skipping")
		}
	}

	return out
}

// drainCompletionAlerts reads pending alerts, pushes the
// AlertTaskCompleted ones, and acknowledges on success. Other alert
// types are left alone — the TUI boot reader is their consumer.
//
// Per-channel failures are logged but don't abort the alert: if at
// least ONE channel succeeds, the alert is acknowledged. This means
// a chronically-broken channel won't block the others, at the cost
// of that broken channel never catching up on backlog. Operator
// problem, not protocol problem.
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
