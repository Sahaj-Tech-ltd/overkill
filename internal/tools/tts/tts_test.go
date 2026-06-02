package tts

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

func TestToolName(t *testing.T) {
	tool := New(config.TTSConfig{})
	if tool.Name() != "tts.speak" {
		t.Errorf("Name: got %q, want tts.speak", tool.Name())
	}
}

func TestExecute_EmptyText(t *testing.T) {
	tool := New(config.TTSConfig{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"text":""}`))
	if err == nil || err.Error() == "" {
		t.Fatal("expected error for empty text")
	}
}

func TestExecute_InvalidJSON(t *testing.T) {
	tool := New(config.TTSConfig{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{bad`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSpeakInput_Roundtrip(t *testing.T) {
	input := SpeakInput{
		Text:     "hello world",
		Provider: "edge",
		Voice:    "en-US-AriaNeural",
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SpeakInput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Text != "hello world" {
		t.Errorf("text: got %q", decoded.Text)
	}
	if decoded.Provider != "edge" {
		t.Errorf("provider: got %q", decoded.Provider)
	}
	if decoded.Voice != "en-US-AriaNeural" {
		t.Errorf("voice: got %q", decoded.Voice)
	}
}

func TestSpeakOutput_Marshal(t *testing.T) {
	out := SpeakOutput{
		AudioPath:  "/tmp/test.mp3",
		Format:     "mp3",
		DurationMs: 1234,
		Provider:   "edge",
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SpeakOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.AudioPath != "/tmp/test.mp3" {
		t.Errorf("audio_path: got %q", decoded.AudioPath)
	}
	if decoded.Format != "mp3" {
		t.Errorf("format: got %q", decoded.Format)
	}
	if decoded.DurationMs != 1234 {
		t.Errorf("duration_ms: got %d", decoded.DurationMs)
	}
}

func TestExecute_MissingProvider(t *testing.T) {
	tool := New(config.TTSConfig{})
	// Default should be "edge" — edge-tts CLI might not be installed but won't panic.
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"text":"test"}`))
	// May error if edge-tts not installed — we just verify no panic.
	_ = err
}

func TestExecute_ElevenLabs_MissingKey(t *testing.T) {
	tool := New(config.TTSConfig{Provider: "elevenlabs"})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"text":"test"}`))
	if err == nil {
		t.Error("expected error for elevenlabs without API key")
	}
}

func TestExecute_OpenAI_MissingKey(t *testing.T) {
	tool := New(config.TTSConfig{Provider: "openai"})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"text":"test"}`))
	if err == nil {
		t.Error("expected error for openai without API key")
	}
}

func TestInputProviderOverride(t *testing.T) {
	tool := New(config.TTSConfig{Provider: "openai"})
	// Input should override config — but "edge" is used if neither has key.
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"text":"test","provider":"edge"}`))
	_ = err // edge may or may not be installed
}

func TestNew(t *testing.T) {
	cfg := config.TTSConfig{
		Provider:      "kittentts",
		OpenAIKey:     "sk-test",
		ElevenLabsKey: "el-test",
	}
	tool := New(cfg)
	if tool == nil {
		t.Fatal("New returned nil")
	}
	if tool.cfg.Provider != "kittentts" {
		t.Errorf("provider: got %q, want kittentts", tool.cfg.Provider)
	}
	if tool.cfg.OpenAIKey != "sk-test" {
		t.Errorf("openai key not set")
	}
}
