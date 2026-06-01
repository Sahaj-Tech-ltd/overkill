// Package signal implements a Signal messaging gateway via signal-cli's
// REST API mode. Run signal-cli as a daemon with `signal-cli daemon --rest-api`
// and point RestAPIURL at it (default http://localhost:8080).
//
// Receive: polls GET /v1/receive/{account} every 5s, converts envelopes to
// gateway.Inbound, and hands them to the dispatcher.
//
// Send: POST /v2/send with JSON {number, message, account}. Update is a
// no-op (signal-cli can't edit messages); Final re-sends with the complete
// text to replace the temporary placeholder.
//
// Health: checks GET /v1/about returns 200 within 2s.
package signal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

// Bot implements gateway.Channel for Signal via signal-cli REST API.
type Bot struct {
	RestAPIURL string // e.g. "http://localhost:8080"
	Account    string // E.164 phone number
	AuthToken  string // Bearer token for signal-cli REST API auth
	Dispatcher *gateway.Dispatcher
	Logger     *log.Logger
	PollEvery  time.Duration // receive poll interval; default 5s
	SeenTTL    time.Duration // dedup TTL; default 10min (from SignalConfig.SeenTTLSec)

	// AllowedNumbers restricts inbound messages to specific E.164 numbers.
	// Empty map (default) means all numbers are allowed.
	AllowedNumbers map[string]bool

	httpClient *http.Client

	// ClientTimeout overrides the default HTTP client timeout (10s).
	// Set to 0 to use the default.
	ClientTimeout time.Duration

	// runCtx carries the Run context for dispatching, so in-flight
	// handlers survive only as long as the bot is alive.
	runCtx   context.Context
	runCtxMu sync.Mutex

	// seen deduplicates messages by content hash + sender, avoiding
	// the fragility of timestamp-based dedup (out-of-order deliveries,
	// clock skew). Eviction-capped at seenCap.
	seenMu sync.Mutex
	seen   map[string]time.Time
}

const (
	// DefaultRESTURL is the default signal-cli REST API endpoint.
	DefaultRESTURL = "http://localhost:8080"
	// seenCap bounds the dedup map size.
	signalSeenCap = 4096
	// seenTTL caps how long a message hash is remembered.
	signalSeenTTL = 10 * time.Minute
)

// alreadySeen reports whether a message with this content hash was
// already processed. Records the hash either way.
func (b *Bot) alreadySeen(hash string) bool {
	b.seenMu.Lock()
	defer b.seenMu.Unlock()
	if b.seen == nil {
		b.seen = make(map[string]time.Time, 64)
	}
	now := time.Now()
	ttl := b.SeenTTL
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	if ts, ok := b.seen[hash]; ok && now.Sub(ts) < ttl {
		return true
	}
	// Evict stale entries when at capacity.
	if len(b.seen) >= signalSeenCap {
		for k, ts := range b.seen {
			if now.Sub(ts) > ttl {
				delete(b.seen, k)
			}
		}
		if len(b.seen) >= signalSeenCap {
			for k := range b.seen {
				delete(b.seen, k)
				break
			}
		}
	}
	b.seen[hash] = now
	return false
}

// NewBot returns a Bot wired to the given config and dispatcher.
func NewBot(restAPIURL, account, authToken string, d *gateway.Dispatcher) *Bot {
	timeout := 10 * time.Second
	return &Bot{
		RestAPIURL: restAPIURL,
		Account:    account,
		AuthToken:  authToken,
		Dispatcher: d,
		Logger:     log.New(io.Discard, "", 0),
		PollEvery:  5 * time.Second,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "signal" }

// Reconnect implements gateway.Reconnecter (B135). The poll loop in Run
// already handles reconnection with backoff; this provides an explicit
// hook for external health monitors.
func (b *Bot) Reconnect(ctx context.Context) error { return nil }

// setAuth sets the Authorization header if an auth token is configured.
func (b *Bot) setAuth(req *http.Request) {
	if b.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+b.AuthToken)
	}
}

// getHTTPClient returns the bot's HTTP client. Never mutates the shared
// http.Client after construction, avoiding a data race on httpClient.Timeout
// when ClientTimeout is set after NewBot (e.g. from SignalConfig).
func (b *Bot) getHTTPClient() *http.Client {
	if b.ClientTimeout > 0 {
		return &http.Client{Timeout: b.ClientTimeout}
	}
	return b.httpClient
}

// Run drives the receive poll loop until ctx is cancelled.
// After maxConsecutiveErrors (10) consecutive failures the error
// surfaces rather than retrying forever (e.g. on wrong auth token).
func (b *Bot) Run(ctx context.Context) error {
	const maxConsecutiveErrors = 10
	b.runCtxMu.Lock()
	b.runCtx = ctx
	b.runCtxMu.Unlock()

	backoff := time.Second
	consecutiveErrors := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msgs, err := b.receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			consecutiveErrors++
			b.Logger.Printf("signal: receive: %v (retry in %s, %d/%d)", err, backoff, consecutiveErrors, maxConsecutiveErrors)
			if consecutiveErrors >= maxConsecutiveErrors {
				return fmt.Errorf("signal: receive failed %d times, giving up: %w", consecutiveErrors, err)
			}
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
		consecutiveErrors = 0
		backoff = time.Second

		for _, m := range msgs {
			b.handle(ctx, m)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(b.PollEvery):
		}
	}
}

// Healthy implements gateway.HealthChecker. Returns true when the
// signal-cli REST API responds to GET /v1/about within 2s.
func (b *Bot) Healthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.RestAPIURL+"/v1/about", nil)
	if err != nil {
		return false
	}
	b.setAuth(req)
	resp, err := b.getHTTPClient().Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ------- receive structures -------

type signalEnvelope struct {
	Envelope struct {
		Source       string `json:"source"`
		SourceNumber string `json:"sourceNumber"`
		SourceName   string `json:"sourceName"`
		DataMessage  *struct {
			Timestamp int64  `json:"timestamp"`
			Message   string `json:"message"`
		} `json:"dataMessage"`
	} `json:"envelope"`
	Account string `json:"account"`
}

func (b *Bot) receive(ctx context.Context) ([]signalEnvelope, error) {
	url := fmt.Sprintf("%s/v1/receive/%s", strings.TrimRight(b.RestAPIURL, "/"), b.Account)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	b.setAuth(req)
	resp, err := b.getHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("signal: receive returned %d: %s", resp.StatusCode, string(body))
	}

	var out []signalEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("signal: decoding receive: %w", err)
	}
	return out, nil
}

func (b *Bot) handle(ctx context.Context, env signalEnvelope) {
	if env.Envelope.DataMessage == nil {
		return
	}
	dm := env.Envelope.DataMessage
	if dm.Message == "" {
		return
	}

	// Allowlist check: if AllowedNumbers is configured, only accept
	// messages from listed numbers.
	if len(b.AllowedNumbers) > 0 && !b.AllowedNumbers[env.Envelope.SourceNumber] {
		b.Logger.Printf("signal: skip disallowed source %s", env.Envelope.SourceNumber)
		return
	}

	// Deduplicate by content hash (sender + text) instead of timestamp,
	// which is fragile with out-of-order deliveries or clock skew.
	hash := fmt.Sprintf("%s:%s:%d", env.Envelope.SourceNumber, dm.Message, dm.Timestamp)
	if b.alreadySeen(hash) {
		return
	}

	from := env.Envelope.SourceName
	if from == "" {
		from = env.Envelope.SourceNumber
		if from == "" {
			from = env.Envelope.Source
		}
	}

	in := gateway.Inbound{
		Channel:  "signal",
		ChatKey:  "signal:" + env.Envelope.SourceNumber,
		From:     from,
		Text:     dm.Message,
		IsDirect: true, // signal-cli REST treats all messages as direct
	}

	reply := &signalReply{bot: b, number: env.Envelope.SourceNumber}
	b.runCtxMu.Lock()
	dispatchCtx := b.runCtx
	b.runCtxMu.Unlock()
	if dispatchCtx == nil {
		dispatchCtx = context.Background()
	}
	go b.Dispatcher.Handle(dispatchCtx, in, reply)
}

// ------- send -------

type sendRequest struct {
	Number  string `json:"number"`
	Message string `json:"message"`
	Account string `json:"account"`
}

func (b *Bot) send(ctx context.Context, number, message string) (string, error) {
	body := sendRequest{
		Number:  number,
		Message: message,
		Account: b.Account,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("signal: marshal send: %w", err)
	}

	url := strings.TrimRight(b.RestAPIURL, "/") + "/v2/send"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	b.setAuth(req)

	resp, err := b.getHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("signal: send returned %d: %s", resp.StatusCode, string(respBody))
	}

	// signal-cli v2/send returns a timestamp string.
	ts := strings.TrimSpace(string(respBody))
	if ts == "" {
		ts = strconv.FormatInt(time.Now().UnixMilli(), 10)
	}
	return ts, nil
}

// ------- signalReply (gateway.Reply) -------

type signalReply struct {
	bot    *Bot
	number string
}

// PostInitial sends a temporary "thinking" message and returns a handle.
func (r *signalReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
	if text == "" {
		text = "⏳ thinking…"
	}
	handle, err := r.bot.send(ctx, r.number, text)
	return handle, err
}

// Update is a no-op — signal-cli has no edit-message API.
func (r *signalReply) Update(ctx context.Context, handle, text string) error {
	return nil
}

// Final re-sends the message with the full response text. The original
// "thinking" placeholder stays visible (no edit API), but the final
// message is what users actually read.
func (r *signalReply) Final(ctx context.Context, handle, text string) error {
	if text == "" {
		text = "(no output)"
	}
	text = gateway.TruncateMessage(text, 65536) // data message limit
	_, err := r.bot.send(ctx, r.number, text)
	return err
}

// Error sends an error indicator to the user.
func (r *signalReply) Error(ctx context.Context, handle string, err error) error {
	_, sendErr := r.bot.send(ctx, r.number, "⚠️ "+err.Error())
	return sendErr
}

// StartTyping returns a no-op stop function — signal-cli REST API has no
// native typing indicator.
func (r *signalReply) StartTyping() (stop func()) {
	return func() {}
}
