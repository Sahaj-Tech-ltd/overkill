package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	matrixbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/matrix"
	signalbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/signal"
	slackbot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/slack"
	telegrambot "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/telegram"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// notifyBots captures references to the bot instances the gateway
// command constructs so the §7.1 Layer 6 completion-push poller can
// deliver alerts through their already-open connections (instead of
// duplicating auth + sessions). Each field is independently nil-
// safe: a channel that's disabled, missing credentials, or hasn't
// finished its Run handshake just skips its send branch.
type notifyBots struct {
	telegramClient        *telegrambot.Client
	discordNotifier       gateway.Notifier
	whatsmeowNotifier     gateway.Notifier
	whatsappCloudNotifier gateway.Notifier
	mattermostNotifier    gateway.Notifier
	slackBot              *slackbot.Bot
	matrixBot             *matrixbot.Bot
	signalBot             *signalbot.Bot
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
				client = telegrambot.New(token)
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
		if bots.discordNotifier == nil {
			logger.Printf("notify-poller: discord notify_channel_id set but discord bot not in this hub; skipping")
		} else {
			notifier := bots.discordNotifier
			channelID := dc.NotifyChannelID
			out = append(out, notifySender{
				name: "discord",
				send: func(ctx context.Context, text string) error {
					return notifier.Notify(ctx, channelID, text)
				},
			})
			logger.Printf("notify-poller: discord channel %s", channelID)
		}
	}

	if mm := cfg.Gateways.Mattermost; mm.NotifyChannelID != "" {
		if bots.mattermostNotifier == nil {
			logger.Printf("notify-poller: mattermost notify_channel_id set but mattermost bot not in this hub; skipping")
		} else {
			notifier := bots.mattermostNotifier
			channelID := mm.NotifyChannelID
			out = append(out, notifySender{
				name: "mattermost",
				send: func(ctx context.Context, text string) error {
					return notifier.Notify(ctx, channelID, text)
				},
			})
			logger.Printf("notify-poller: mattermost channel %s", channelID)
		}
	}

	if sl := cfg.Gateways.Slack; sl.NotifyChannelID != "" {
		// Slack Bot has no Notify() method — always use REST API.
		token := sl.BotToken
		if token == "" {
			token = os.Getenv("SLACK_BOT_TOKEN")
		}
		if token != "" {
			channelID := sl.NotifyChannelID
			out = append(out, notifySender{
				name: "slack",
				send: func(ctx context.Context, text string) error {
					return sendSlackREST(ctx, token, channelID, text)
				},
			})
			logger.Printf("notify-poller: slack (REST) channel %s", channelID)
		}
	}

	if mx := cfg.Gateways.Matrix; mx.NotifyRoomID != "" {
		out = append(out, notifySender{
			name: "matrix",
			send: func(ctx context.Context, text string) error {
				return sendMatrixREST(ctx, mx.HomeserverURL, mx.AccessToken, mx.NotifyRoomID, text)
			},
		})
		logger.Printf("notify-poller: matrix room %s", mx.NotifyRoomID)
	}

	if sg := cfg.Gateways.Signal; sg.NotifyNumber != "" {
		restURL := sg.RestAPIURL
		if restURL == "" {
			restURL = signalbot.DefaultRESTURL
		}
		out = append(out, notifySender{
			name: "signal",
			send: func(ctx context.Context, text string) error {
				return sendSignalREST(ctx, restURL, sg.Account, sg.NotifyNumber, text)
			},
		})
		logger.Printf("notify-poller: signal number %s", sg.NotifyNumber)
	}

	if wa := cfg.Gateways.WhatsApp; wa.NotifyJID != "" {
		switch {
		case bots.whatsmeowNotifier != nil:
			notifier := bots.whatsmeowNotifier
			jid := wa.NotifyJID
			out = append(out, notifySender{
				name: "whatsapp/whatsmeow",
				send: func(ctx context.Context, text string) error {
					return notifier.Notify(ctx, jid, text)
				},
			})
			logger.Printf("notify-poller: whatsapp/whatsmeow jid %s", jid)
		case bots.whatsappCloudNotifier != nil:
			notifier := bots.whatsappCloudNotifier
			jid := wa.NotifyJID
			out = append(out, notifySender{
				name: "whatsapp/cloud",
				send: func(ctx context.Context, text string) error {
					return notifier.Notify(ctx, jid, text)
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

// ── REST-based notify helpers for gateways without bot instances ──

func sendSlackREST(ctx context.Context, token, channelID, text string) error {
	body := fmt.Sprintf(`{"channel":%q,"text":%q}`, channelID, text)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/chat.postMessage", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack: HTTP %d", resp.StatusCode)
	}
	// Slack returns HTTP 200 for API errors — check the ok field.
	var slackResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&slackResp); err == nil && !slackResp.OK {
		return fmt.Errorf("slack: %s", slackResp.Error)
	}
	return nil
}

func sendMatrixREST(ctx context.Context, homeserverURL, accessToken, roomID, text string) error {
	url := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message", homeserverURL, roomID)
	body := fmt.Sprintf(`{"msgtype":"m.text","body":%q}`, text)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("matrix: HTTP %d", resp.StatusCode)
	}
	return nil
}

func sendSignalREST(ctx context.Context, restAPIURL, account, number, text string) error {
	url := fmt.Sprintf("%s/v2/send", restAPIURL)
	body := fmt.Sprintf(`{"number":%q,"message":%q,"account":%q}`, number, text, account)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("signal: HTTP %d", resp.StatusCode)
	}
	return nil
}
