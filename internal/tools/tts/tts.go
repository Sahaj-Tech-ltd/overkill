// Package tts provides text-to-speech tools for Overkill.
//
// Supported providers:
//   - edge: Microsoft Edge TTS (free, no API key, via edge-tts CLI)
//   - kittentts: Local KittenTTS (free, via Python + ffmpeg)
//   - openai: OpenAI TTS API
//   - elevenlabs: ElevenLabs TTS API
package tts

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// Tool implements tools.Tool for text-to-speech synthesis.
type Tool struct {
	cfg config.TTSConfig
}

// New creates a new TTS tool with the given config.
func New(cfg config.TTSConfig) *Tool {
	return &Tool{cfg: cfg}
}

func (t *Tool) Name() string { return "tts.speak" }

// SpeakInput is the JSON input for the tts.speak tool.
type SpeakInput struct {
	Text     string `json:"text"`
	Provider string `json:"provider"` // "edge" | "kittentts" | "openai" | "elevenlabs"
	Voice    string `json:"voice"`    // optional per-call voice override
}

// SpeakOutput is the JSON output from the tts.speak tool.
type SpeakOutput struct {
	AudioPath  string `json:"audio_path"`
	Format     string `json:"format"` // "mp3" | "ogg"
	DurationMs int    `json:"duration_ms"`
	Provider   string `json:"provider"`
}

func (t *Tool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in SpeakInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("tts.speak: %w", err)
	}

	if in.Text == "" {
		return nil, fmt.Errorf("tts.speak: text is required")
	}

	// Resolve provider: input > config > "edge"
	provider := in.Provider
	if provider == "" {
		provider = t.cfg.Provider
	}
	if provider == "" {
		provider = "edge"
	}

	switch provider {
	case "edge":
		return t.speakEdge(ctx, in.Text, in.Voice)
	case "kittentts":
		return t.speakKittenTTS(ctx, in.Text)
	case "openai":
		return t.speakOpenAI(ctx, in.Text, in.Voice)
	case "elevenlabs":
		return t.speakElevenLabs(ctx, in.Text, in.Voice)
	default:
		return nil, fmt.Errorf("tts.speak: unknown provider %q (supported: edge, kittentts, openai, elevenlabs)", provider)
	}
}

// ---------------------------------------------------------------------------
// edge-tts provider
// ---------------------------------------------------------------------------

func (t *Tool) speakEdge(ctx context.Context, text, voice string) (json.RawMessage, error) {
	if _, err := exec.LookPath("edge-tts"); err != nil {
		return nil, fmt.Errorf("tts.speak: edge-tts not installed — install with: pip install edge-tts")
	}

	if voice == "" {
		voice = t.resolveVoice("en-US-AriaNeural")
	}

	outPath := tmpPath("overkill-tts", ".mp3")

	args := []string{
		"--voice", voice,
		"--text", text,
		"--write-media", outPath,
	}

	cmd := exec.CommandContext(ctx, "edge-tts", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tts.speak (edge): %v: %s", err, stderr.String())
	}

	return marshalOut(SpeakOutput{
		AudioPath: outPath,
		Format:    "mp3",
		Provider:  "edge",
	})
}

// ---------------------------------------------------------------------------
// KittenTTS provider
// ---------------------------------------------------------------------------

func (t *Tool) speakKittenTTS(ctx context.Context, text string) (json.RawMessage, error) {
	// Check for ffmpeg
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("tts.speak: ffmpeg not installed — required for KittenTTS audio conversion")
	}

	wavPath := tmpPath("overkill-tts", ".wav")
	oggPath := tmpPath("overkill-tts", ".ogg")

	// Write text to temp file to avoid Python injection via string interpolation
	textPath := tmpPath("overkill-tts-text", ".txt")
	if err := os.WriteFile(textPath, []byte(text), 0600); err != nil {
		return nil, fmt.Errorf("tts.speak: failed to write text file: %w", err)
	}
	defer os.Remove(textPath)

	// Run Python KittenTTS generation (reads text from file, not string interpolation)
	pythonCmd := fmt.Sprintf(
		"from kittentts import TTS; import numpy as np; import soundfile as sf; tts=TTS(); text=open('%s', 'r').read(); audio=tts.generate(text); sf.write('%s', audio, 24000)",
		textPath, wavPath,
	)
	cmd := exec.CommandContext(ctx, "python3", "-c", pythonCmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tts.speak (kittentts): python generation failed: %v: %s", err, stderr.String())
	}

	// Convert to OGG with ffmpeg
	ffCmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", wavPath,
		"-c:a", "libopus",
		"-b:a", "32k",
		oggPath,
		"-y",
	)
	ffCmd.Stderr = &stderr
	if err := ffCmd.Run(); err != nil {
		_ = os.Remove(wavPath)
		return nil, fmt.Errorf("tts.speak (kittentts): ffmpeg conversion failed: %v: %s", err, stderr.String())
	}

	// Clean up wav
	_ = os.Remove(wavPath)

	return marshalOut(SpeakOutput{
		AudioPath: oggPath,
		Format:    "ogg",
		Provider:  "kittentts",
	})
}

// ---------------------------------------------------------------------------
// OpenAI TTS provider
// ---------------------------------------------------------------------------

func (t *Tool) speakOpenAI(ctx context.Context, text, voice string) (json.RawMessage, error) {
	apiKey := t.cfg.OpenAIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("tts.speak: OpenAI API key not configured (set [tts] openai_key or OPENAI_API_KEY)")
	}

	if voice == "" {
		voice = t.resolveVoice("alloy")
	}

	body := map[string]interface{}{
		"model": "tts-1",
		"voice": voice,
		"input": text,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("tts.speak (openai): marshal: %w", err)
	}

	ttsURL := os.Getenv("OPENAI_TTS_URL")
	if ttsURL == "" {
		ttsURL = "https://api.openai.com/v1/audio/speech"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ttsURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("tts.speak (openai): %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts.speak (openai): request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("tts.speak (openai): HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	outPath := tmpPath("overkill-tts", ".mp3")
	f, err := os.Create(outPath)
	if err != nil {
		return nil, fmt.Errorf("tts.speak (openai): create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return nil, fmt.Errorf("tts.speak (openai): write: %w", err)
	}

	return marshalOut(SpeakOutput{
		AudioPath: outPath,
		Format:    "mp3",
		Provider:  "openai",
	})
}

// ---------------------------------------------------------------------------
// ElevenLabs TTS provider
// ---------------------------------------------------------------------------

func (t *Tool) speakElevenLabs(ctx context.Context, text, voice string) (json.RawMessage, error) {
	apiKey := t.cfg.ElevenLabsKey
	if apiKey == "" {
		apiKey = os.Getenv("ELEVENLABS_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("tts.speak: ElevenLabs API key not configured (set [tts] elevenlabs_key or ELEVENLABS_API_KEY)")
	}

	voiceID := voice
	if voiceID == "" {
		voiceID = t.resolveVoice("21m00Tcm4TlvDq8ikWAM") // "Rachel" default
	}

	body := map[string]interface{}{
		"text":     text,
		"model_id": "eleven_monolingual_v1",
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("tts.speak (elevenlabs): marshal: %w", err)
	}

	elevenBase := os.Getenv("ELEVENLABS_BASE_URL")
	if elevenBase == "" {
		elevenBase = "https://api.elevenlabs.io"
	}
	url := fmt.Sprintf("%s/v1/text-to-speech/%s", elevenBase, voiceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("tts.speak (elevenlabs): %w", err)
	}
	req.Header.Set("xi-api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts.speak (elevenlabs): request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tts.speak (elevenlabs): HTTP %d: %s", resp.StatusCode, string(errBody))
	}

	outPath := tmpPath("overkill-tts", ".mp3")
	f, err := os.Create(outPath)
	if err != nil {
		return nil, fmt.Errorf("tts.speak (elevenlabs): create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return nil, fmt.Errorf("tts.speak (elevenlabs): write: %w", err)
	}

	return marshalOut(SpeakOutput{
		AudioPath: outPath,
		Format:    "mp3",
		Provider:  "elevenlabs",
	})
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// resolveVoice returns the user-specified voice, or the config default, or
// the provided fallback.
func (t *Tool) resolveVoice(fallback string) string {
	if t.cfg.Voice != "" {
		return t.cfg.Voice
	}
	return fallback
}

// tmpPath generates a unique temp file path with the given extension.
func tmpPath(prefix, ext string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	suffix := hex.EncodeToString(b)
	return filepath.Join(os.TempDir(), prefix+"-"+suffix+ext)
}

// marshalOut serializes a SpeakOutput, handling errors.
func marshalOut(out SpeakOutput) (json.RawMessage, error) {
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("tts.speak: marshal output: %w", err)
	}
	return raw, nil
}
