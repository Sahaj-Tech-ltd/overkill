package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// SlackAPI is the minimal subset of Slack's Web API we use. Defining it as an
// interface lets bot_test.go drop in an httptest-backed fake without ever
// touching the real Slack network.
type SlackAPI interface {
	PostMessage(ctx context.Context, channel, threadTS, text string) (msgTS string, err error)
	UpdateMessage(ctx context.Context, channel, ts, text string) error
	AddReaction(ctx context.Context, channel, ts, name string) error
	RemoveReaction(ctx context.Context, channel, ts, name string) error
}

// HTTPSlackAPI is the production implementation — talks to slack.com over HTTPS.
type HTTPSlackAPI struct {
	BotToken string
	BaseURL  string // override for tests; defaults to https://slack.com/api
	HTTP     *http.Client
}

// NewHTTPSlackAPI returns a client with sensible timeouts.
func NewHTTPSlackAPI(botToken string) *HTTPSlackAPI {
	return &HTTPSlackAPI{
		BotToken: botToken,
		BaseURL:  "https://slack.com/api",
		HTTP:     &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *HTTPSlackAPI) endpoint(method string) string {
	base := c.BaseURL
	if base == "" {
		base = "https://slack.com/api"
	}
	return base + "/" + method
}

// callJSON POSTs a JSON body to a Slack Web API method using bearer auth.
// We use JSON (not form-encoded) so unicode survives untouched.
func (c *HTTPSlackAPI) callJSON(ctx context.Context, method string, body any, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.BotToken)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("slack: %s: %w", method, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("slack: %s: http %d: %s", method, resp.StatusCode, string(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("slack: %s: decode: %w", method, err)
		}
	}
	return nil
}

// PostMessage posts a new message. threadTS may be empty.
func (c *HTTPSlackAPI) PostMessage(ctx context.Context, channel, threadTS, text string) (string, error) {
	body := map[string]any{
		"channel": channel,
		"text":    text,
	}
	if threadTS != "" {
		body["thread_ts"] = threadTS
	}
	var resp postMessageResponse
	if err := c.callJSON(ctx, "chat.postMessage", body, &resp); err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("slack: chat.postMessage: %s", resp.Error)
	}
	return resp.TS, nil
}

// UpdateMessage edits a previously-posted message in place.
func (c *HTTPSlackAPI) UpdateMessage(ctx context.Context, channel, ts, text string) error {
	body := map[string]any{
		"channel": channel,
		"ts":      ts,
		"text":    text,
	}
	var resp postMessageResponse
	if err := c.callJSON(ctx, "chat.update", body, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("slack: chat.update: %s", resp.Error)
	}
	return nil
}

// AddReaction adds an emoji reaction. `name` is the bare emoji name without
// surrounding colons ("white_check_mark", not ":white_check_mark:").
func (c *HTTPSlackAPI) AddReaction(ctx context.Context, channel, ts, name string) error {
	body := map[string]any{"channel": channel, "timestamp": ts, "name": name}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := c.callJSON(ctx, "reactions.add", body, &resp); err != nil {
		return err
	}
	// already_reacted is a no-op, not an error.
	if !resp.OK && resp.Error != "already_reacted" {
		return fmt.Errorf("slack: reactions.add: %s", resp.Error)
	}
	return nil
}

// RemoveReaction removes an emoji reaction.
func (c *HTTPSlackAPI) RemoveReaction(ctx context.Context, channel, ts, name string) error {
	body := map[string]any{"channel": channel, "timestamp": ts, "name": name}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := c.callJSON(ctx, "reactions.remove", body, &resp); err != nil {
		return err
	}
	if !resp.OK && resp.Error != "no_reaction" {
		return fmt.Errorf("slack: reactions.remove: %s", resp.Error)
	}
	return nil
}

// openConnection calls apps.connections.open with the App-Level Token to
// obtain a Socket-Mode WebSocket URL. App-Level token (xapp-...) is
// distinct from the Bot Token (xoxb-...) and must be passed as a bearer.
func openConnection(ctx context.Context, baseURL, appToken string, httpClient *http.Client) (string, error) {
	if baseURL == "" {
		baseURL = "https://slack.com/api"
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/apps.connections.open", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var out connectionsOpenResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("slack: apps.connections.open: decode: %w", err)
	}
	if !out.OK {
		return "", fmt.Errorf("slack: apps.connections.open: %s", out.Error)
	}
	if _, err := url.Parse(out.URL); err != nil {
		return "", fmt.Errorf("slack: apps.connections.open: bad url: %w", err)
	}
	return out.URL, nil
}
