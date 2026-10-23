package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client speaks HTTP/SSE to a remote ACP agent. An injected HTTP client is
// reused unchanged, including its transport, connection pool and policies.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// DefaultRequestTimeout bounds non-streaming requests, including body reads,
// when the caller has not supplied a deadline. SSE follows the caller context.
const DefaultRequestTimeout = 30 * time.Second
const maxEventBytes = 1024 * 1024
const maxResponseBytes = 1024 * 1024

func NewClient(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{}}
}

func requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultRequestTimeout)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.HasSuffix(path, "/events") {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, requestError(ctx, err)
	}
	// Preserve the injected RoundTripper while also releasing body readers
	// whose implementation relies on Close rather than request cancellation.
	bodyCloser := &contextBody{ReadCloser: resp.Body}
	bodyCloser.stop = context.AfterFunc(ctx, func() { bodyCloser.closeUnderlying() })
	resp.Body = bodyCloser
	return resp, nil
}

type contextBody struct {
	io.ReadCloser
	stop func() bool
	once sync.Once
	err  error
}

func (body *contextBody) Close() error {
	body.stop()
	return body.closeUnderlying()
}

func (body *contextBody) closeUnderlying() error {
	body.once.Do(func() { body.err = body.ReadCloser.Close() })
	return body.err
}

func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func decodeResponse(ctx context.Context, body io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return requestError(ctx, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("acp/client: response exceeds %d bytes", maxResponseBytes)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return json.Unmarshal(data, target)
}

func (c *Client) GetInfo(ctx context.Context) (Info, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	resp, err := c.request(ctx, http.MethodGet, "/v1/info", nil)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Info{}, fmt.Errorf("acp/client: info status %d", resp.StatusCode)
	}
	var info Info
	if err := decodeResponse(ctx, resp.Body, &info); err != nil {
		return Info{}, err
	}
	return info, nil
}

func (c *Client) ListSessions(ctx context.Context) ([]json.RawMessage, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	resp, err := c.request(ctx, http.MethodGet, "/v1/sessions", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("acp/client: sessions status %d", resp.StatusCode)
	}
	var sessions []json.RawMessage
	if err := decodeResponse(ctx, resp.Body, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

// Send preserves the default-session API. Cancel ctx if you stop reading events.
func (c *Client) Send(ctx context.Context, content string) (<-chan Event, error) {
	return c.SendTo(ctx, "", content)
}

// SendTo addresses an existing remote session, or creates one when sessionID is
// empty. The POST is bounded independently of the subsequent agent stream.
func (c *Client) SendTo(ctx context.Context, sessionID, content string) (<-chan Event, error) {
	messageID, err := c.sendMessage(ctx, SendRequest{From: "overkill-client", Content: content, SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	return c.Stream(ctx, messageID)
}

func (c *Client) sendMessage(ctx context.Context, message SendRequest) (string, error) {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	resp, err := c.request(ctx, http.MethodPost, "/v1/messages", message)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("acp/client: send status %d", resp.StatusCode)
	}
	var sent SendResponse
	if err := decodeResponse(ctx, resp.Body, &sent); err != nil {
		return "", err
	}
	if sent.MessageID == "" {
		return "", fmt.Errorf("acp/client: response has no messageID")
	}
	return sent.MessageID, nil
}

// Stream delivers SSE frames until one done/error event. A malformed or dropped
// connection produces one terminal error instead of appearing successful.
// Callers MUST cancel ctx when abandoning the receiver. Cancellation releases
// blocked sends and closes the HTTP body. If the buffer is full during failure,
// its oldest pending nonterminal event is displaced to retain the terminal error.
func (c *Client) Stream(ctx context.Context, messageID string) (<-chan Event, error) {
	if messageID == "" {
		return nil, fmt.Errorf("acp/client: messageID is required")
	}
	streamCtx, cancel := context.WithCancel(ctx)
	resp, err := c.request(streamCtx, http.MethodGet, "/v1/messages/"+url.PathEscape(messageID)+"/events", nil)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("acp/client: stream status %d", resp.StatusCode)
	}
	out := make(chan Event, 32)
	go func() {
		defer close(out)
		defer cancel()
		defer resp.Body.Close()
		terminalFailure := func(err error) {
			event := Event{Type: "error", Error: "acp/client: stream failed: " + err.Error(), Timestamp: time.Now().UTC()}
			select {
			case out <- event:
				return
			default:
			}
			select {
			case <-out:
			default:
			}
			out <- event // Only this goroutine produces events; a slot is now available.
		}
		publish := func(event Event) bool {
			if err := streamCtx.Err(); err != nil {
				terminalFailure(err)
				return false
			}
			select {
			case out <- event:
				return true
			case <-streamCtx.Done():
				terminalFailure(streamCtx.Err())
				return false
			}
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), maxEventBytes)
		var data []string
		size := 0
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if len(data) == 0 {
					continue
				}
				var event Event
				if err := json.Unmarshal([]byte(strings.Join(data, "\n")), &event); err != nil {
					terminalFailure(fmt.Errorf("invalid SSE JSON: %w", err))
					return
				}
				switch event.Type {
				case "text_delta", "tool_call", "done", "error":
				default:
					terminalFailure(fmt.Errorf("unsupported or missing event type %q", event.Type))
					return
				}
				if event.Type == "error" && event.Error == "" {
					event.Error = "remote agent returned an unspecified error"
				}
				if !publish(event) {
					return
				}
				if event.Type == "done" || event.Type == "error" {
					return
				}
				data = nil
				size = 0
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			value := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			size += len(value) + 1
			if size > maxEventBytes {
				terminalFailure(fmt.Errorf("SSE event exceeds %d bytes", maxEventBytes))
				return
			}
			data = append(data, value)
		}
		if err := streamCtx.Err(); err != nil {
			terminalFailure(err)
		} else if err := scanner.Err(); err != nil {
			terminalFailure(fmt.Errorf("reading SSE: %w", err))
		} else {
			terminalFailure(io.ErrUnexpectedEOF)
		}
	}()
	return out, nil
}

func (c *Client) Cancel(ctx context.Context, messageID string) error {
	ctx, cancel := requestContext(ctx)
	defer cancel()
	resp, err := c.request(ctx, http.MethodPost, "/v1/messages/"+url.PathEscape(messageID)+"/cancel", struct{}{})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("acp/client: cancel status %d", resp.StatusCode)
	}
	count, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return requestError(ctx, err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if count > maxResponseBytes {
		return fmt.Errorf("acp/client: cancel response exceeds %d bytes", maxResponseBytes)
	}
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.GetInfo(ctx)
	return err
}
