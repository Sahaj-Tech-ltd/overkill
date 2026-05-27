// Package matrix implements a Matrix messaging gateway via raw HTTP
// against the Matrix Client-Server API (no heavy SDK).
//
// Receive: long-poll GET /_matrix/client/v3/sync with since-token
// tracking. Parses m.room.message events from joined rooms, filters
// out own messages, and dispatches them as gateway.Inbound.
//
// Send: PUT /_matrix/client/v3/rooms/{roomId}/send/m.room.message/{txnId}
// with m.text msgtype. PostInitial sends the dispatcher-supplied text
// (typically a "⏳ thinking…" placeholder); Final sends the complete
// response as a new message since Matrix simple messages can't be
// edited in-place.
//
// Health: checks sync was within 90s and no permanent auth error.
package matrix

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

// Bot implements gateway.Channel for Matrix via raw HTTP.
type Bot struct {
	HomeserverURL string // e.g. "https://matrix.org"
	UserID        string // @user:homeserver (may be empty if Password auto-login fills it)
	AccessToken   string
	Password      string // for auto-login if AccessToken is empty
	Dispatcher    *gateway.Dispatcher
	Logger        *log.Logger

	httpClient *http.Client

	// sync state
	since    string
	lastSync time.Time
	mu       sync.Mutex

	// permanent auth error — Healthy() returns false once this is set
	authErr error
	authMu  sync.RWMutex

	// room member count cache (roomID → count); used for IsDirect
	memberCounts map[string]int
	memberMu     sync.RWMutex
}

// NewBot returns a Bot wired to the given config and dispatcher.
func NewBot(homeserverURL, userID, accessToken, password string, d *gateway.Dispatcher) *Bot {
	return &Bot{
		HomeserverURL: homeserverURL,
		UserID:        userID,
		AccessToken:   accessToken,
		Password:      password,
		Dispatcher:    d,
		Logger:        log.New(io.Discard, "", 0),
		httpClient:    &http.Client{Timeout: 60 * time.Second},
		memberCounts:  make(map[string]int),
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "matrix" }

// Run drives the sync loop until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	// Login if needed.
	if err := b.login(ctx); err != nil {
		b.setAuthErr(err)
		return err
	}

	// Initial sync to get the since token.
	syncResp, err := b.doSync(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.since = syncResp.NextBatch
	b.lastSync = time.Now()
	b.mu.Unlock()

	b.processEvents(syncResp)

	// Sync loop with exponential backoff on errors.
	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		syncResp, err = b.doSync(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			b.Logger.Printf("matrix: sync error: %v (retry in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second

		b.mu.Lock()
		b.since = syncResp.NextBatch
		b.lastSync = time.Now()
		b.mu.Unlock()

		b.processEvents(syncResp)
	}
}

// Healthy implements gateway.HealthChecker. Returns true when the last
// successful sync was within 90 seconds and no permanent auth error is set.
func (b *Bot) Healthy() bool {
	b.authMu.RLock()
	authErr := b.authErr
	b.authMu.RUnlock()
	if authErr != nil {
		return false
	}

	b.mu.Lock()
	last := b.lastSync
	b.mu.Unlock()
	if last.IsZero() {
		return false
	}
	return time.Since(last) < 90*time.Second
}

// ---------------------------------------------------------------------------
// login
// ---------------------------------------------------------------------------

func (b *Bot) login(ctx context.Context) error {
	if b.AccessToken != "" {
		return nil
	}
	if b.Password == "" {
		return fmt.Errorf("matrix: no access token and no password — cannot login")
	}

	body := map[string]string{
		"type":     "m.login.password",
		"user":     b.UserID,
		"password": b.Password,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("matrix login marshal: %w", err)
	}

	url := strings.TrimRight(b.HomeserverURL, "/") + "/_matrix/client/v3/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("matrix login: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("matrix login: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("matrix login: %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		UserID      string `json:"user_id"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("matrix login decode: %w", err)
	}

	b.UserID = result.UserID
	b.AccessToken = result.AccessToken
	b.Logger.Printf("matrix: logged in as %s", b.UserID)
	return nil
}

// ---------------------------------------------------------------------------
// sync
// ---------------------------------------------------------------------------

// matrixSyncResponse is the subset of the Matrix /sync response we care about.
type matrixSyncResponse struct {
	NextBatch string                     `json:"next_batch"`
	Rooms     matrixSyncRooms            `json:"rooms"`
}

type matrixSyncRooms struct {
	Join map[string]matrixJoinedRoom `json:"join"`
}

type matrixJoinedRoom struct {
	Timeline matrixTimeline `json:"timeline"`
}

type matrixTimeline struct {
	Events []matrixEvent `json:"events"`
}

type matrixEvent struct {
	Type     string           `json:"type"`
	Sender   string           `json:"sender"`
	EventID  string           `json:"event_id"`
	Content  matrixContent    `json:"content"`
}

type matrixContent struct {
	Body    string `json:"body"`
	MsgType string `json:"msgtype"`
}

func (b *Bot) doSync(ctx context.Context) (*matrixSyncResponse, error) {
	url := strings.TrimRight(b.HomeserverURL, "/") + "/_matrix/client/v3/sync"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("matrix sync: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.AccessToken)

	b.mu.Lock()
	since := b.since
	b.mu.Unlock()

	q := req.URL.Query()
	q.Set("timeout", "30000")
	if since != "" {
		q.Set("since", since)
	}
	req.URL.RawQuery = q.Encode()

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("matrix sync: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		authErr := fmt.Errorf("matrix auth error: %d: %s", resp.StatusCode, string(body))
		b.setAuthErr(authErr)
		return nil, authErr
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("matrix sync: %d: %s", resp.StatusCode, string(body))
	}

	var syncResp matrixSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		return nil, fmt.Errorf("matrix sync decode: %w", err)
	}

	return &syncResp, nil
}

func (b *Bot) setAuthErr(err error) {
	b.authMu.Lock()
	b.authErr = err
	b.authMu.Unlock()
}

// ---------------------------------------------------------------------------
// event processing
// ---------------------------------------------------------------------------

func (b *Bot) processEvents(syncResp *matrixSyncResponse) {
	if syncResp == nil {
		return
	}
	for roomID, room := range syncResp.Rooms.Join {
		for _, event := range room.Timeline.Events {
			if event.Type != "m.room.message" {
				continue
			}
			if event.Sender == b.UserID {
				continue
			}
			text := strings.TrimSpace(event.Content.Body)
			if text == "" {
				continue
			}

			isDirect := b.isDirectRoom(roomID)

			in := gateway.Inbound{
				Channel:  "matrix",
				ChatKey:  roomID,
				From:     event.Sender,
				Text:     text,
				IsDirect: isDirect,
			}

			reply := &matrixReply{bot: b, roomID: roomID}
			go b.Dispatcher.Handle(context.Background(), in, reply)
		}
	}
}

// isDirectRoom returns true if the room has exactly 2 joined members.
// Results are cached in-memory for the lifetime of the bot.
func (b *Bot) isDirectRoom(roomID string) bool {
	b.memberMu.RLock()
	count, ok := b.memberCounts[roomID]
	b.memberMu.RUnlock()
	if ok {
		return count == 2
	}

	count = b.fetchMemberCount(roomID)

	b.memberMu.Lock()
	b.memberCounts[roomID] = count
	b.memberMu.Unlock()

	return count == 2
}

func (b *Bot) fetchMemberCount(roomID string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/joined_members",
		strings.TrimRight(b.HomeserverURL, "/"), roomID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		b.Logger.Printf("matrix: fetch members for %s: %v", roomID, err)
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+b.AccessToken)

	resp, err := b.httpClient.Do(req)
	if err != nil {
		b.Logger.Printf("matrix: fetch members for %s: %v", roomID, err)
		return 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0
	}

	var result struct {
		Joined map[string]json.RawMessage `json:"joined"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0
	}
	return len(result.Joined)
}

// ---------------------------------------------------------------------------
// send
// ---------------------------------------------------------------------------

// sendMessage PUTs an m.text message to a room and returns the event ID.
func (b *Bot) sendMessage(ctx context.Context, roomID, text string) (string, error) {
	txnID := randomTxnID()
	url := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%s",
		strings.TrimRight(b.HomeserverURL, "/"), roomID, txnID)

	body := map[string]string{
		"msgtype": "m.text",
		"body":    text,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("matrix send marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("matrix send: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("matrix send: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("matrix send: %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("matrix send decode: %w", err)
	}

	return result.EventID, nil
}

// ---------------------------------------------------------------------------
// matrixReply (gateway.Reply)
// ---------------------------------------------------------------------------

type matrixReply struct {
	bot    *Bot
	roomID string
}

// PostInitial sends the dispatcher-provided text (usually "⏳ thinking…")
// as a placeholder. Matrix has no edit-in-place for simple messages
// (m.replace needs a relation field), so Final sends the full response
// as a separate message.
func (r *matrixReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
	if text == "" {
		text = "⏳ thinking…"
	}
	handle, err := r.bot.sendMessage(ctx, r.roomID, text)
	return handle, err
}

// Update is a no-op — Matrix simple m.text messages can't be edited
// without an m.replace relation, which we skip for MVP simplicity.
func (r *matrixReply) Update(ctx context.Context, handle, text string) error {
	return nil
}

// Final sends the complete response text. Since we can't edit the
// placeholder message, the user sees two messages: the thinking
// indicator followed by the final response.
func (r *matrixReply) Final(ctx context.Context, handle, text string) error {
	if text == "" {
		text = "(no output)"
	}
	_, err := r.bot.sendMessage(ctx, r.roomID, text)
	return err
}

// Error sends an error indicator to the user.
func (r *matrixReply) Error(ctx context.Context, handle string, err error) error {
	_, sendErr := r.bot.sendMessage(ctx, r.roomID, "⚠️ "+err.Error())
	return sendErr
}

// StartTyping returns a no-op stop function — Matrix /typing endpoint
// is skipped for MVP simplicity.
func (r *matrixReply) StartTyping() (stop func()) {
	return func() {}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func randomTxnID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
