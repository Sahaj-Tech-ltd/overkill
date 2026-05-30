// Package vision turns image bytes into prose a text-only main agent
// can reason over (master plan §7.4). Used by remote gateways to
// inline-describe photos and by the vision_describe tool to caption
// browser screenshots.
//
// We deliberately keep this separate from the providers package: the
// main agent's Message type is plain string today, and bolting image
// content blocks onto every provider for a single feature would be a
// large refactor for marginal gain. Instead a Describer takes bytes
// in, returns text out, and the caller injects the text wherever it
// needs it.
package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Image is one input frame to describe. Mime is the standard MIME type
// (image/png, image/jpeg, image/webp, image/gif). Empty Mime defaults
// to image/png — the dev browser screenshots are always PNG.
type Image struct {
	Bytes []byte
	Mime  string
}

// Describer turns one or more images plus an optional steering prompt
// into prose. Implementations MUST be safe to call concurrently.
type Describer interface {
	Describe(ctx context.Context, images []Image, prompt string) (string, error)
}

// AnthropicDescriber is the production Describer. Talks straight to
// the Messages API rather than going through the providers abstraction
// because we need image content blocks the providers.Message type
// doesn't carry yet.
type AnthropicDescriber struct {
	APIKey    string
	Model     string        // e.g. "claude-sonnet-4-5-20250929"
	BaseURL   string        // override for tests; default https://api.anthropic.com
	HTTP      *http.Client  // overridable
	MaxTokens int           // default 1024
	Timeout   time.Duration // default 30s
}

// NewAnthropic returns a describer with sane defaults.
func NewAnthropic(apiKey, model string) *AnthropicDescriber {
	return &AnthropicDescriber{
		APIKey:    apiKey,
		Model:     model,
		BaseURL:   "https://api.anthropic.com",
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		MaxTokens: 1024,
		Timeout:   30 * time.Second,
	}
}

// Describe sends the images + prompt and returns the model's text. Any
// non-2xx surfaces with the response body for debuggability.
func (d *AnthropicDescriber) Describe(ctx context.Context, images []Image, prompt string) (string, error) {
	if d.APIKey == "" {
		return "", fmt.Errorf("vision: no api key")
	}
	if d.Model == "" {
		return "", fmt.Errorf("vision: no model")
	}
	if len(images) == 0 {
		return "", fmt.Errorf("vision: no images")
	}

	if prompt == "" {
		prompt = "Describe this image in 2-4 sentences. Focus on what would matter to a software engineer reviewing it (UI elements, error states, code, diagrams). If it looks like a webpage, name the page. If text is visible, transcribe the key parts."
	}

	content := make([]map[string]any, 0, len(images)+1)
	for _, img := range images {
		mime := img.Mime
		if mime == "" {
			mime = "image/png"
		}
		content = append(content, map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mime,
				"data":       base64.StdEncoding.EncodeToString(img.Bytes),
			},
		})
	}
	content = append(content, map[string]any{"type": "text", "text": prompt})

	body, err := json.Marshal(map[string]any{
		"model":      d.Model,
		"max_tokens": d.maxTokens(),
		"messages": []map[string]any{
			{"role": "user", "content": content},
		},
	})
	if err != nil {
		return "", err
	}

	callCtx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, d.baseURL()+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", d.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	httpClient := d.HTTP
	if httpClient == nil {
		// B022: Default to client with explicit 30s timeout instead of
		// http.DefaultClient, which has no timeout and could hang forever.
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vision: anthropic: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("vision: anthropic: http %d: %s", resp.StatusCode, string(raw))
	}

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("vision: parse: %w", err)
	}
	var b strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(c.Text)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("vision: empty response")
	}
	return out, nil
}

func (d *AnthropicDescriber) baseURL() string {
	if d.BaseURL != "" {
		return d.BaseURL
	}
	return "https://api.anthropic.com"
}
func (d *AnthropicDescriber) maxTokens() int {
	if d.MaxTokens > 0 {
		return d.MaxTokens
	}
	return 1024
}
func (d *AnthropicDescriber) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return 30 * time.Second
}

// MIMEFromBytes sniffs PNG/JPEG/GIF/WebP magic so callers downloading
// arbitrary files don't have to guess. Falls back to image/png.
func MIMEFromBytes(b []byte) string {
	switch {
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "image/gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	default:
		return "image/png"
	}
}
