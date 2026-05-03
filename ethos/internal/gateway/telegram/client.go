// Package telegram is a minimal Bot API client used by the Telegram
// gateway. Pure net/http so we don't pull in a vendored bot framework.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL points at Telegram's Bot API.
const DefaultBaseURL = "https://api.telegram.org"

// Client wraps the small slice of the Bot API the gateway needs.
type Client struct {
	Token   string
	BaseURL string // overridable for tests
	HTTP    *http.Client
}

// New returns a client with sensible defaults. A custom HTTP client is
// used so tests can stub the round-tripper.
func New(token string) *Client {
	return &Client{
		Token:   token,
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 70 * time.Second},
	}
}

// Update is one entry from getUpdates.
type Update struct {
	UpdateID int      `json:"update_id"`
	Message  *Message `json:"message"`
}

// Message is the subset of fields we read.
type Message struct {
	MessageID int    `json:"message_id"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
}

// User is a sender; we keep enough to log who sent what.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Chat is a conversation; Type is "private" | "group" | "supergroup".
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// GetUpdates long-polls. offset is the next update_id to fetch (0 on
// first call). The 60-second long-poll keeps webhook-free deployments
// dirt cheap.
func (c *Client) GetUpdates(ctx context.Context, offset int, timeout time.Duration) ([]Update, error) {
	q := url.Values{}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	q.Set("timeout", strconv.Itoa(int(timeout.Seconds())))
	q.Set("allowed_updates", `["message"]`)
	var resp struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
		Desc   string   `json:"description"`
	}
	if err := c.do(ctx, "getUpdates", q, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("telegram: getUpdates: %s", resp.Desc)
	}
	return resp.Result, nil
}

// SendMessage posts a new message and returns the message id.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) (int, error) {
	q := url.Values{}
	q.Set("chat_id", strconv.FormatInt(chatID, 10))
	q.Set("text", text)
	q.Set("disable_web_page_preview", "true")
	var resp struct {
		OK     bool    `json:"ok"`
		Result Message `json:"result"`
		Desc   string  `json:"description"`
	}
	if err := c.do(ctx, "sendMessage", q, &resp); err != nil {
		return 0, err
	}
	if !resp.OK {
		return 0, fmt.Errorf("telegram: sendMessage: %s", resp.Desc)
	}
	return resp.Result.MessageID, nil
}

// EditMessageText updates an existing message in place. Telegram
// rejects edits with identical text — caller dedupes.
func (c *Client) EditMessageText(ctx context.Context, chatID int64, messageID int, text string) error {
	q := url.Values{}
	q.Set("chat_id", strconv.FormatInt(chatID, 10))
	q.Set("message_id", strconv.Itoa(messageID))
	q.Set("text", text)
	q.Set("disable_web_page_preview", "true")
	var resp struct {
		OK   bool   `json:"ok"`
		Desc string `json:"description"`
	}
	if err := c.do(ctx, "editMessageText", q, &resp); err != nil {
		return err
	}
	if !resp.OK {
		// "message is not modified" is harmless; everything else surfaces.
		if strings.Contains(resp.Desc, "not modified") {
			return nil
		}
		return fmt.Errorf("telegram: editMessageText: %s", resp.Desc)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method string, q url.Values, out any) error {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u := fmt.Sprintf("%s/bot%s/%s", base, c.Token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(q.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %s: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telegram: %s: http %d: %s", method, resp.StatusCode, string(body))
	}
	return json.Unmarshal(body, out)
}
