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
	RestAPIURL string        // e.g. "http://localhost:8080"
	Account    string        // E.164 phone number
	Dispatcher *gateway.Dispatcher
	Logger     *log.Logger
	PollEvery  time.Duration // receive poll interval; default 5s

	httpClient *http.Client

	// lastReceived tracks the highest timestamp we've seen to avoid
	// dispatching duplicates across restarts within the same process.
	lastReceived int64
	mu           sync.Mutex
}

// NewBot returns a Bot wired to the given config and dispatcher.
func NewBot(restAPIURL, account string, d *gateway.Dispatcher) *Bot {
	return &Bot{
		RestAPIURL: restAPIURL,
		Account:    account,
		Dispatcher: d,
		Logger:     log.New(io.Discard, "", 0),
		PollEvery:  5 * time.Second,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "signal" }

// Run drives the receive poll loop until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	backoff := time.Second

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		msgs, err := b.receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			b.Logger.Printf("signal: receive: %v (retry in %s)", err, backoff)
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
	resp, err := b.httpClient.Do(req)
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
		Timestamp    int64  `json:"timestamp"`
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
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("signal: receive returned %d: %s", resp.StatusCode, string(body))
	}

	var out []signalEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
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

	ts := dm.Timestamp
	if ts == 0 {
		ts = env.Envelope.Timestamp
	}

	// Deduplicate: ignore messages we've already seen.
	b.mu.Lock()
	if ts <= b.lastReceived {
		b.mu.Unlock()
		return
	}
	b.lastReceived = ts
	b.mu.Unlock()

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
	go b.Dispatcher.Handle(ctx, in, reply)
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

	resp, err := b.httpClient.Do(req)
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
