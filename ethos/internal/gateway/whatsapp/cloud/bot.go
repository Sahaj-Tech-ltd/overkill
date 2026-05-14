// Package cloud — WhatsApp Business Cloud API gateway (Batch L,
// official-path backend).
//
// Two surfaces:
//
//   1. Webhook receiver. Meta POSTs incoming messages here, signed via
//      HMAC-SHA256 in X-Hub-Signature-256. GET requests are the
//      "verify" challenge we echo on first setup.
//
//   2. Send API. Outgoing messages POST to graph.facebook.com with a
//      bearer token. The 24-hour customer-service window applies:
//      free-form messages only work if the user messaged us first
//      within the past 24h. Outside that window only pre-approved
//      template messages send — we surface that as an error rather
//      than silently falling back.
//
// Reply streaming: Cloud API has no edit-in-place. PostInitial sends
// "thinking…", Update no-ops, Final sends the full assembled response
// as a new message. User sees one progress line and one answer; not
// as live as Telegram's edit-each-chunk, but acceptable and consistent
// with how every other Cloud API client behaves.
package cloud

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
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
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

// Bot implements gateway.Channel for WhatsApp Cloud API. One Bot per
// (phone_number_id, access_token, app_secret) tuple. Multiple bots
// can be hubbed if you have multiple WhatsApp Business numbers, but
// typical setups need exactly one.
type Bot struct {
	// PhoneNumberID is the Meta-assigned numeric ID for the WhatsApp
	// Business phone number we send through. Distinct from the
	// E.164 phone number itself.
	PhoneNumberID string
	// AccessToken is the long-lived (or system-user) token used as
	// the bearer for Graph API calls. NOT the app-secret.
	AccessToken string
	// AppSecret is the Meta app's secret used to verify webhook
	// signatures (X-Hub-Signature-256). Required — webhook calls
	// with missing/wrong signatures are dropped.
	AppSecret string
	// VerifyToken is the shared secret echoed on GET /webhook
	// during the one-time verification handshake. Configured by us
	// + repeated by Meta on every verify request.
	VerifyToken string
	// Listen is the HTTP bind address for the webhook server. The
	// caller is responsible for placing this behind a reverse proxy
	// that terminates HTTPS — Meta only delivers webhooks to
	// HTTPS endpoints.
	Listen string

	// GraphURL overrides the Graph API base for tests. Empty falls
	// back to the production URL.
	GraphURL string

	// AllowedFrom optionally restricts which sender numbers the bot
	// will respond to (E.164 form, no leading +). Empty = any.
	AllowedFrom map[string]bool

	Dispatcher *gateway.Dispatcher
	Logger     *log.Logger

	server *http.Server
}

// NewBot returns a Bot wired with the required Cloud API credentials.
// Empty PhoneNumberID/AccessToken/AppSecret/VerifyToken/Listen each
// surface as a clear error from Run — we don't fail the constructor
// because the config-loading layer often builds the Bot before
// final validation.
func NewBot(phoneNumberID, accessToken, appSecret, verifyToken, listen string, allowedFrom []string, d *gateway.Dispatcher) *Bot {
	allow := make(map[string]bool, len(allowedFrom))
	for _, p := range allowedFrom {
		allow[strings.TrimPrefix(strings.TrimSpace(p), "+")] = true
	}
	return &Bot{
		PhoneNumberID: phoneNumberID,
		AccessToken:   accessToken,
		AppSecret:     appSecret,
		VerifyToken:   verifyToken,
		Listen:        listen,
		AllowedFrom:   allow,
		Dispatcher:    d,
		Logger:        log.New(io.Discard, "", 0),
	}
}

// Name implements gateway.Channel.
func (b *Bot) Name() string { return "whatsapp-cloud" }

// Notify sends an unsolicited WhatsApp message via the Cloud API.
// `to` is the recipient phone number in E.164 form (no leading +).
// WhatsApp enforces a 24-hour messaging window: free-form outbound
// messages outside an active conversation will be rejected by Meta
// unless they fit an approved template. The §7.1 Layer 6 task
// alerts are free-form; they'll succeed when sent during the
// window and fail (logged by the poller) otherwise.
func (b *Bot) Notify(ctx context.Context, to, text string) error {
	if to == "" {
		return fmt.Errorf("whatsapp-cloud: notify: to required")
	}
	return b.sendMessage(ctx, to, text)
}

// Run starts the webhook HTTP server and blocks until ctx is
// cancelled. Returns ctx.Err() on cancel; wrapped errors for missing
// config or listen failures.
func (b *Bot) Run(ctx context.Context) error {
	if err := b.validate(); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", b.handleWebhook)
	b.server = &http.Server{
		Addr:              b.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	b.Logger.Printf("whatsapp-cloud: listening on %s", b.Listen)

	// Shut down on ctx cancellation. Done on a goroutine so Run can
	// block on ListenAndServe.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = b.server.Shutdown(shutdownCtx)
	}()
	err := b.server.ListenAndServe()
	if err == http.ErrServerClosed {
		return ctx.Err()
	}
	return err
}

func (b *Bot) validate() error {
	switch {
	case b.PhoneNumberID == "":
		return fmt.Errorf("whatsapp-cloud: phone_number_id required")
	case b.AccessToken == "":
		return fmt.Errorf("whatsapp-cloud: access_token required")
	case b.AppSecret == "":
		return fmt.Errorf("whatsapp-cloud: app_secret required (webhook signature verification)")
	case b.VerifyToken == "":
		return fmt.Errorf("whatsapp-cloud: verify_token required")
	case b.Listen == "":
		return fmt.Errorf("whatsapp-cloud: listen address required")
	}
	return nil
}

// handleWebhook routes Meta's GET verification challenge and POST
// message-delivery requests.
func (b *Bot) handleWebhook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		b.handleVerify(w, r)
	case http.MethodPost:
		b.handleMessage(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleVerify implements Meta's one-time webhook handshake. They
// send GET ?hub.mode=subscribe&hub.verify_token=X&hub.challenge=Y;
// we echo Y back if X matches our configured token.
//
// We deliberately don't constant-time the comparison — the verify
// token isn't a per-request secret, the value is shared with Meta
// and stable. Timing leaks would still require Meta to be the
// attacker, in which case we have bigger problems.
func (b *Bot) handleVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("hub.mode") != "subscribe" {
		http.Error(w, "unexpected mode", http.StatusBadRequest)
		return
	}
	if q.Get("hub.verify_token") != b.VerifyToken {
		http.Error(w, "verify token mismatch", http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(q.Get("hub.challenge")))
}

// handleMessage processes incoming webhook deliveries. Verifies the
// signature before parsing — a payload without a valid signature is
// either misconfigured or a spoof attempt; both get rejected with
// 401 so Meta surfaces the failure.
func (b *Bot) handleMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB cap
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if !verifySignature(body, b.AppSecret, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "signature mismatch", http.StatusUnauthorized)
		return
	}

	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "parse body", http.StatusBadRequest)
		return
	}

	// Acknowledge before processing so Meta doesn't time out and
	// retry. The dispatcher work happens on a goroutine.
	w.WriteHeader(http.StatusOK)
	go b.processPayload(payload)
}

// processPayload walks the nested webhook structure and surfaces each
// message to the dispatcher. Meta batches multiple messages per
// delivery so we iterate; one bad message doesn't stop the rest.
func (b *Bot) processPayload(p webhookPayload) {
	for _, entry := range p.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				b.processMessage(msg, change.Value)
			}
		}
	}
}

// processMessage converts one cloud-API message into gateway.Inbound
// and hands it to the dispatcher. Image messages need a follow-up
// media-fetch round-trip; text messages go through directly.
func (b *Bot) processMessage(msg cloudMessage, env cloudValue) {
	from := strings.TrimPrefix(msg.From, "+")
	if len(b.AllowedFrom) > 0 && !b.AllowedFrom[from] {
		b.Logger.Printf("whatsapp-cloud: drop disallowed sender %s", from)
		return
	}

	text := strings.TrimSpace(msg.Text.Body)
	in := gateway.Inbound{
		Channel:  "whatsapp",
		ChatKey:  from, // one chat per phone number
		From:     from,
		Text:     text,
		IsDirect: true, // WhatsApp Cloud is always 1:1 or business reply
	}

	// Image: Cloud API gives us a media ID we have to fetch.
	if msg.Image != nil && msg.Image.ID != "" {
		if img, err := b.fetchMedia(msg.Image.ID); err != nil {
			b.Logger.Printf("whatsapp-cloud: fetch image %s: %v", msg.Image.ID, err)
		} else {
			in.Images = append(in.Images, img)
			if in.Text == "" && msg.Image.Caption != "" {
				in.Text = strings.TrimSpace(msg.Image.Caption)
			}
		}
	}

	if in.Text == "" && len(in.Images) == 0 {
		return
	}

	reply := &cloudReply{bot: b, to: from}
	go b.Dispatcher.Handle(context.Background(), in, reply)
}

// fetchMedia is the two-step download: GET the media metadata to get
// a temporary URL, then GET that URL with the bearer token.
func (b *Bot) fetchMedia(mediaID string) (gateway.InboundImage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Step 1: media metadata.
	metaURL := b.graphURL() + "/" + mediaID
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	req.Header.Set("Authorization", "Bearer "+b.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return gateway.InboundImage{}, fmt.Errorf("metadata: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return gateway.InboundImage{}, fmt.Errorf("metadata status %d", resp.StatusCode)
	}
	var meta struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return gateway.InboundImage{}, fmt.Errorf("metadata parse: %w", err)
	}
	if meta.URL == "" {
		return gateway.InboundImage{}, fmt.Errorf("no media url in response")
	}

	// Step 2: the bytes.
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, meta.URL, nil)
	req2.Header.Set("Authorization", "Bearer "+b.AccessToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return gateway.InboundImage{}, fmt.Errorf("download: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return gateway.InboundImage{}, fmt.Errorf("download status %d", resp2.StatusCode)
	}
	const cap = 16 * 1024 * 1024 // WhatsApp's image limit per their docs
	bytes, err := io.ReadAll(io.LimitReader(resp2.Body, cap))
	if err != nil {
		return gateway.InboundImage{}, fmt.Errorf("read: %w", err)
	}
	mime := meta.MimeType
	if mime == "" {
		mime = vision.MIMEFromBytes(bytes)
	}
	return gateway.InboundImage{Bytes: bytes, Mime: mime}, nil
}

func (b *Bot) graphURL() string {
	if b.GraphURL != "" {
		return b.GraphURL
	}
	return "https://graph.facebook.com/v18.0"
}

// sendMessage posts a text message to the Cloud API.
func (b *Bot) sendMessage(ctx context.Context, to, text string) error {
	if text == "" {
		text = " " // Cloud API rejects empty bodies
	}
	body := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": text},
	}
	data, _ := json.Marshal(body)
	url := b.graphURL() + "/" + b.PhoneNumberID + "/messages"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+b.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("send status %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}
	return nil
}

// verifySignature computes the expected HMAC-SHA256 of body using
// appSecret and compares it to the header value. Format: "sha256=<hex>".
func verifySignature(body []byte, appSecret, header string) bool {
	if !strings.HasPrefix(header, "sha256=") {
		return false
	}
	wantHex := strings.TrimPrefix(header, "sha256=")
	wantSig, err := hex.DecodeString(wantHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	gotSig := mac.Sum(nil)
	// Constant-time comparison — signature verification IS the per-
	// request secret check; timing leaks matter here, unlike the
	// verify-token handshake.
	return hmac.Equal(wantSig, gotSig)
}

// cloudReply implements gateway.Reply for Cloud API. PostInitial
// sends a "thinking" placeholder, Update no-ops (Cloud API has no
// edit-in-place), Final sends the assembled response as a new
// message. User sees one progress line + one answer.
type cloudReply struct {
	bot *Bot
	to  string

	mu       sync.Mutex
	notified bool
}

func (r *cloudReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
	// First reply: send a brief acknowledgment so the user knows the
	// message landed. If the dispatcher's initial text is itself a
	// short slash-command response (e.g. "bookmarked: x"), send that
	// instead so we don't double-message.
	if text == "" {
		text = "⏳ thinking…"
	}
	if err := r.bot.sendMessage(ctx, r.to, text); err != nil {
		return "", err
	}
	r.mu.Lock()
	r.notified = true
	r.mu.Unlock()
	return "ack", nil
}

func (r *cloudReply) Update(_ context.Context, _ string, _ string) error {
	// Intentional no-op. Cloud API has no edit-in-place and sending
	// every chunk would spam the user with N partial messages.
	return nil
}

func (r *cloudReply) Final(ctx context.Context, _ string, text string) error {
	return r.bot.sendMessage(ctx, r.to, text)
}

func (r *cloudReply) Error(ctx context.Context, _ string, err error) error {
	return r.bot.sendMessage(ctx, r.to, "⚠️ "+err.Error())
}

// webhookPayload is the slice of Meta's webhook JSON we care about.
// The full schema has 30+ fields we don't use; we narrow to what
// the dispatcher needs.
type webhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Field string     `json:"field"`
			Value cloudValue `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type cloudValue struct {
	MessagingProduct string         `json:"messaging_product"`
	Metadata         cloudMetadata  `json:"metadata"`
	Contacts         []cloudContact `json:"contacts"`
	Messages         []cloudMessage `json:"messages"`
}

type cloudMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type cloudContact struct {
	WaID    string `json:"wa_id"`
	Profile struct {
		Name string `json:"name"`
	} `json:"profile"`
}

type cloudMessage struct {
	From      string    `json:"from"`
	ID        string    `json:"id"`
	Timestamp string    `json:"timestamp"`
	Type      string    `json:"type"`
	Text      cloudText `json:"text"`
	Image     *cloudImg `json:"image,omitempty"`
}

type cloudText struct {
	Body string `json:"body"`
}

type cloudImg struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Caption  string `json:"caption"`
	SHA256   string `json:"sha256"`
}
