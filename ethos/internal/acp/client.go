package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client speaks to a remote ACP server (another overkill, claude code, opencode,
// or any custom agent that implements the protocol).
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a Client with sensible defaults.
func NewClient(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: 0}}
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	cli := c.HTTP
	if cli == nil {
		cli = http.DefaultClient
	}
	return cli.Do(req)
}

// GetInfo returns the remote /v1/info payload.
func (c *Client) GetInfo(ctx context.Context) (Info, error) {
	resp, err := c.request(ctx, http.MethodGet, "/v1/info", nil)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Info{}, fmt.Errorf("acp/client: info status %d", resp.StatusCode)
	}
	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return Info{}, err
	}
	return info, nil
}

// ListSessions calls GET /v1/sessions.
func (c *Client) ListSessions(ctx context.Context) ([]json.RawMessage, error) {
	resp, err := c.request(ctx, http.MethodGet, "/v1/sessions", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("acp/client: sessions status %d", resp.StatusCode)
	}
	var out []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// Send posts a message and returns a channel of streamed Events. The caller
// is responsible for cancelling ctx when done.
func (c *Client) Send(ctx context.Context, content string) (<-chan Event, error) {
	resp, err := c.request(ctx, http.MethodPost, "/v1/messages", SendRequest{
		From: "overkill-client", Content: content,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("acp/client: send status %d", resp.StatusCode)
	}
	var sr SendResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, err
	}
	return c.Stream(ctx, sr.MessageID)
}

// Stream attaches an SSE listener to /v1/messages/{id}/events.
func (c *Client) Stream(ctx context.Context, messageID string) (<-chan Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/messages/"+messageID+"/events", nil)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "text/event-stream")
	cli := c.HTTP
	if cli == nil {
		cli = &http.Client{Timeout: 0}
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("acp/client: stream status %d", resp.StatusCode)
	}

	out := make(chan Event, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		// SSE lines can be larger than the default scanner buffer.
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			var ev Event
			if err := json.Unmarshal([]byte(data), &ev); err == nil {
				out <- ev
				if ev.Type == "done" || ev.Type == "error" {
					return
				}
			}
		}
	}()
	return out, nil
}

// Cancel posts to /v1/messages/{id}/cancel.
func (c *Client) Cancel(ctx context.Context, messageID string) error {
	resp, err := c.request(ctx, http.MethodPost, "/v1/messages/"+messageID+"/cancel", struct{}{})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("acp/client: cancel status %d", resp.StatusCode)
	}
	return nil
}

// Ping verifies the remote is up by calling GetInfo with a short timeout.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.GetInfo(ctx)
	return err
}
