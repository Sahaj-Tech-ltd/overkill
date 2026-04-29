package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type WebTool struct {
	client  *http.Client
	maxSize int64
}

type WebInput struct {
	URL     string `json:"url"`
	Format  string `json:"format"`
	MaxSize int    `json:"max_size"`
}

type WebOutput struct {
	URL         string `json:"url"`
	Content     string `json:"content"`
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type"`
	Truncated   bool   `json:"truncated"`
}

func NewWebTool() *WebTool {
	return &WebTool{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		maxSize: 5 * 1024 * 1024,
	}
}

func (w *WebTool) Name() string {
	return "web"
}

func (w *WebTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in WebInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}

	if in.URL == "" {
		return nil, fmt.Errorf("web: url is required")
	}

	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return nil, fmt.Errorf("web: only http and https schemes are allowed")
	}

	maxSize := w.maxSize
	if in.MaxSize > 0 {
		maxSize = int64(in.MaxSize)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	req.Header.Set("User-Agent", "Ethos/1.0")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")

	limited := io.LimitReader(resp.Body, maxSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}

	truncated := len(data) > int(maxSize)
	if truncated {
		data = data[:maxSize]
	}

	output := WebOutput{
		URL:         in.URL,
		Content:     string(data),
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		Truncated:   truncated,
	}

	raw, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	return raw, nil
}
