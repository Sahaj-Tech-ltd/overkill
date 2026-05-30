// Package telegram is a minimal Bot API client used by the Telegram
// gateway. Pure net/http so we don't pull in a vendored bot framework.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	MessageID int         `json:"message_id"`
	Date      int64       `json:"date"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Photo     []PhotoSize `json:"photo"`
	From      *User       `json:"from"`
	Chat      Chat        `json:"chat"`
}

// PhotoSize is one resolution of a photo upload. Telegram returns an
// array sorted smallest-to-largest; we always pick the last (largest).
type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int    `json:"file_size"`
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
	// Subscribe to text + photo (caption arrives in same message).
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

// GetFile resolves a file_id to a downloadable file_path. Telegram
// caches file_paths for ~1 hour; callers should download promptly.
func (c *Client) GetFile(ctx context.Context, fileID string) (string, error) {
	q := url.Values{}
	q.Set("file_id", fileID)
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
		Desc string `json:"description"`
	}
	if err := c.do(ctx, "getFile", q, &resp); err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("telegram: getFile: %s", resp.Desc)
	}
	return resp.Result.FilePath, nil
}

// DownloadFile fetches the bytes for a file_path returned by GetFile.
// The /file/bot<token>/<path> URL is documented and stable.
func (c *Client) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u := fmt.Sprintf("%s/file/bot%s/%s", base, c.Token, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return nil, fmt.Errorf("telegram: download: http %d: %s", resp.StatusCode, string(body))
	}
	// Cap downloads at 16 MiB — well above any reasonable photo size and
	// keeps a malicious bot operator from filling memory.
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// BotCommand is one entry in the command menu shown in Telegram's input bar.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetMyCommands replaces the bot's command list for all chats.
func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	cmdBytes, err := json.Marshal(commands)
	if err != nil {
		return fmt.Errorf("telegram: marshal commands: %w", err)
	}
	q := url.Values{}
	q.Set("commands", string(cmdBytes))
	var resp struct {
		OK   bool   `json:"ok"`
		Desc string `json:"description"`
	}
	if err := c.do(ctx, "setMyCommands", q, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("telegram: setMyCommands: %s", resp.Desc)
	}
	return nil
}

// DeleteMyCommands removes all commands (for shutdown cleanup).
func (c *Client) DeleteMyCommands(ctx context.Context) error {
	var resp struct {
		OK   bool   `json:"ok"`
		Desc string `json:"description"`
	}
	if err := c.do(ctx, "deleteMyCommands", url.Values{}, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("telegram: deleteMyCommands: %s", resp.Desc)
	}
	return nil
}

// SendChatAction shows a status action in the chat (typing, upload_photo, etc.).
// The indicator auto-expires after 5 seconds — callers should refresh.
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
	q := url.Values{}
	q.Set("chat_id", strconv.FormatInt(chatID, 10))
	q.Set("action", action)
	var resp struct {
		OK   bool   `json:"ok"`
		Desc string `json:"description"`
	}
	if err := c.do(ctx, "sendChatAction", q, &resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("telegram: sendChatAction: %s", resp.Desc)
	}
	return nil
}

// SendVoice sends an OGG audio file as a Telegram voice note.
// oggPath must be a local .ogg file encoded with libopus at a reasonable
// bitrate (e.g. 32k). Telegram plays it as a native voice bubble.
func (c *Client) SendVoice(ctx context.Context, chatID int64, oggPath string) (int, error) {
	f, err := os.Open(oggPath)
	if err != nil {
		return 0, fmt.Errorf("telegram: sendVoice: open: %w", err)
	}
	defer f.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	_ = writer.WriteField("chat_id", strconv.FormatInt(chatID, 10))

	part, err := writer.CreateFormFile("voice", filepath.Base(oggPath))
	if err != nil {
		return 0, fmt.Errorf("telegram: sendVoice: create form file: %w", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return 0, fmt.Errorf("telegram: sendVoice: copy: %w", err)
	}
	if err := writer.Close(); err != nil {
		return 0, fmt.Errorf("telegram: sendVoice: close writer: %w", err)
	}

	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u := fmt.Sprintf("%s/bot%s/sendVoice", base, c.Token)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &body)
	if err != nil {
		// B131: Redact token from request-creation errors.
		sanitized := strings.ReplaceAll(err.Error(), c.Token, "***")
		return 0, fmt.Errorf("telegram: sendVoice: %s", sanitized)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("telegram: sendVoice: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("telegram: sendVoice: http %d: %s", resp.StatusCode, string(respBody))
	}

	var out struct {
		OK     bool    `json:"ok"`
		Result Message `json:"result"`
		Desc   string  `json:"description"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return 0, err
	}
	if !out.OK {
		return 0, fmt.Errorf("telegram: sendVoice: %s", out.Desc)
	}
	return out.Result.MessageID, nil
}

func (c *Client) do(ctx context.Context, method string, q url.Values, out any) error {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u := fmt.Sprintf("%s/bot%s/%s", base, c.Token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(q.Encode()))
	if err != nil {
		// B131: Redact the token from errors — http.NewRequestWithContext may
		// include the full URL in its error string. Replace with ***.
		sanitized := strings.ReplaceAll(err.Error(), c.Token, "***")
		return fmt.Errorf("telegram: %s: %s", method, sanitized)
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
