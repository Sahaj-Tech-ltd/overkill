package messaging

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

func TestName(t *testing.T) {
	cfg := config.GatewayConfig{}
	tool := New(cfg)
	if tool.Name() != "send_message" {
		t.Errorf("Name() = %q, want 'send_message'", tool.Name())
	}
}

// --- input validation ---

func TestExecuteEmptyInput(t *testing.T) {
	tool := New(config.GatewayConfig{})
	_, err := tool.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil input")
	}
	if !strings.Contains(err.Error(), "invalid input") {
		t.Errorf("expected 'invalid input' in error, got: %v", err)
	}
}

func TestExecuteInvalidJSON(t *testing.T) {
	tool := New(config.GatewayConfig{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{notjson`))
	if err == nil {
		t.Fatal("expected error for invalid JSON input")
	}
	if !strings.Contains(err.Error(), "invalid input") {
		t.Errorf("expected 'invalid input' in error, got: %v", err)
	}
}

func TestExecuteEmptyPlatform(t *testing.T) {
	tool := New(config.GatewayConfig{})
	input := json.RawMessage(`{"platform":"","target":"123","message":"hello"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty platform")
	}
	if !strings.Contains(err.Error(), "platform is required") {
		t.Errorf("expected 'platform is required' in error, got: %v", err)
	}
}

func TestExecuteEmptyTarget(t *testing.T) {
	tool := New(config.GatewayConfig{})
	input := json.RawMessage(`{"platform":"telegram","target":"","message":"hello"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty target")
	}
	if !strings.Contains(err.Error(), "target is required") {
		t.Errorf("expected 'target is required' in error, got: %v", err)
	}
}

func TestExecuteEmptyMessage(t *testing.T) {
	tool := New(config.GatewayConfig{})
	input := json.RawMessage(`{"platform":"telegram","target":"123","message":""}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty message")
	}
	if !strings.Contains(err.Error(), "message is required") {
		t.Errorf("expected 'message is required' in error, got: %v", err)
	}
}

func TestExecuteUnsupportedPlatform(t *testing.T) {
	tool := New(config.GatewayConfig{})
	input := json.RawMessage(`{"platform":"whatsapp","target":"123","message":"hello"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for unsupported platform")
	}
	if !strings.Contains(err.Error(), "unsupported platform") {
		t.Errorf("expected 'unsupported platform' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), `"whatsapp"`) {
		t.Errorf("expected platform name in error, got: %v", err)
	}
}

func TestExecuteTelegramNoToken(t *testing.T) {
	// Config has no bot token → should fail at the send level
	tool := New(config.GatewayConfig{})
	input := json.RawMessage(`{"platform":"telegram","target":"123","message":"hello"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for telegram without bot token")
	}
	if !strings.Contains(err.Error(), "telegram") {
		t.Errorf("expected 'telegram' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bot token") {
		t.Errorf("expected 'bot token' in error, got: %v", err)
	}
}

func TestExecuteTelegramNonNumericTarget(t *testing.T) {
	tool := New(config.GatewayConfig{
		Telegram: config.TelegramConfig{
			BotToken: "test-token",
		},
	})
	input := json.RawMessage(`{"platform":"telegram","target":"not-a-number","message":"hello"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for non-numeric telegram target")
	}
	if !strings.Contains(err.Error(), "numeric chat ID") {
		t.Errorf("expected 'numeric chat ID' in error, got: %v", err)
	}
}

func TestExecuteDiscordNoToken(t *testing.T) {
	tool := New(config.GatewayConfig{})
	input := json.RawMessage(`{"platform":"discord","target":"123","message":"hello"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for discord without bot token")
	}
	if !strings.Contains(err.Error(), "discord") {
		t.Errorf("expected 'discord' in error, got: %v", err)
	}
}

func TestExecuteSlackNoToken(t *testing.T) {
	tool := New(config.GatewayConfig{})
	input := json.RawMessage(`{"platform":"slack","target":"C123","message":"hello"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for slack without bot token")
	}
	if !strings.Contains(err.Error(), "slack") {
		t.Errorf("expected 'slack' in error, got: %v", err)
	}
}

// --- SendInput / SendOutput JSON ---

func TestSendInputMarshal(t *testing.T) {
	in := SendInput{
		Platform: "telegram",
		Target:   "12345",
		Message:  "hello world",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var roundTrip SendInput
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if roundTrip.Platform != "telegram" {
		t.Errorf("Platform = %q", roundTrip.Platform)
	}
	if roundTrip.Target != "12345" {
		t.Errorf("Target = %q", roundTrip.Target)
	}
	if roundTrip.Message != "hello world" {
		t.Errorf("Message = %q", roundTrip.Message)
	}
}

func TestSendOutputMarshal(t *testing.T) {
	out := SendOutput{
		Sent:      true,
		Platform:  "telegram",
		Target:    "12345",
		MessageID: "42",
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var roundTrip SendOutput
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if !roundTrip.Sent {
		t.Error("Sent should be true")
	}
	if roundTrip.Platform != "telegram" {
		t.Errorf("Platform = %q", roundTrip.Platform)
	}
	if roundTrip.MessageID != "42" {
		t.Errorf("MessageID = %q", roundTrip.MessageID)
	}
}

func TestSendOutputOmitEmptyMessageID(t *testing.T) {
	out := SendOutput{
		Sent:     true,
		Platform: "discord",
		Target:   "channel-1",
		// MessageID is empty
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}
	// With omitempty, message_id should not appear when empty
	if strings.Contains(string(raw), `"message_id"`) {
		t.Error("empty message_id should be omitted due to omitempty tag")
	}
}

// --- SendMessageTool construction ---

func TestNew(t *testing.T) {
	cfg := config.GatewayConfig{
		Telegram: config.TelegramConfig{
			BotToken: "test-token-123",
		},
	}
	tool := New(cfg)
	if tool == nil {
		t.Fatal("New() returned nil")
	}
	if tool.Name() != "send_message" {
		t.Errorf("Name() = %q", tool.Name())
	}
}

func TestExecuteCaseInsensitivePlatform(t *testing.T) {
	// Platforms are checked with exact match — "Telegram" with capital T should fail
	tool := New(config.GatewayConfig{})
	input := json.RawMessage(`{"platform":"Telegram","target":"123","message":"hello"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for case-mismatched platform")
	}
	if !strings.Contains(err.Error(), "unsupported platform") {
		t.Errorf("expected 'unsupported platform' for case mismatch, got: %v", err)
	}
}
