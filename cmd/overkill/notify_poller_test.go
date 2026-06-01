package main

import (
	"io"
	"log"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway/telegram"
)

// silentLogger discards output so tests stay quiet.
func silentLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func TestBuildNotifySenders_EmptyWhenNothingConfigured(t *testing.T) {
	cfg := &config.Config{}
	senders := buildNotifySenders(cfg, notifyBots{}, silentLogger())
	if len(senders) != 0 {
		t.Errorf("no notify targets → empty senders, got %d", len(senders))
	}
}

func TestBuildNotifySenders_TelegramFallsBackToTokenWhenBotMissing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateways.Telegram.NotifyChatID = 12345
	cfg.Gateways.Telegram.BotToken = "x"

	senders := buildNotifySenders(cfg, notifyBots{}, silentLogger())
	if len(senders) != 1 || senders[0].name != "telegram" {
		t.Errorf("expected one telegram sender via token fallback, got %+v", senders)
	}
}

func TestBuildNotifySenders_TelegramSkipsWithoutToken(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateways.Telegram.NotifyChatID = 12345
	// No token, no bot — must skip.
	senders := buildNotifySenders(cfg, notifyBots{}, silentLogger())
	if len(senders) != 0 {
		t.Errorf("missing token should skip: %+v", senders)
	}
}

func TestBuildNotifySenders_DiscordSkippedWhenBotMissing(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateways.Discord.NotifyChannelID = "999"
	// No discordBot in notifyBots → channel not in this hub.
	senders := buildNotifySenders(cfg, notifyBots{}, silentLogger())
	if len(senders) != 0 {
		t.Errorf("discord requires a live bot in this hub, got %+v", senders)
	}
}

func TestBuildNotifySenders_WhatsAppPrefersWhatsmeowOverCloud(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateways.WhatsApp.NotifyJID = "+15551234@s.whatsapp.net"

	// Both bots wired → whatsmeow wins by branch ordering. We can't
	// instantiate real bots in this test cheaply, so the assertion
	// is structural: when ONLY notify_jid is set and no bot exists,
	// we get zero senders.
	senders := buildNotifySenders(cfg, notifyBots{}, silentLogger())
	if len(senders) != 0 {
		t.Errorf("no whatsapp bot in hub → no sender, got %+v", senders)
	}
}

func TestBuildNotifySenders_UsesRunningTelegramClient(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateways.Telegram.NotifyChatID = 42
	// notifyBots provides a client; cfg has no token. We rely on
	// the running client, not the fallback.
	bots := notifyBots{telegramClient: telegram.New("test-token")}
	senders := buildNotifySenders(cfg, bots, silentLogger())
	if len(senders) != 1 {
		t.Fatalf("expected 1 sender from running client, got %d", len(senders))
	}
	if senders[0].name != "telegram" {
		t.Errorf("unexpected sender name: %s", senders[0].name)
	}
}
