// Package bridge is a one-size HTTP gateway any sidecar (Baileys for
// WhatsApp, discord.js, an SMS relay) can plug into. The sidecar POSTs
// inbound messages to /v1/in and SSE-subscribes to /v1/out for
// streamed replies. Two endpoints, no library coupling.
//
// Wire format is intentionally minimal — channel, chat, text in,
// {handle, kind, text} out — so a 30-line Node script is enough to
// adapt any chat platform.
package bridge

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

// InboundPayload is the JSON shape sidecars POST to /v1/in.
type InboundPayload struct {
	Channel  string            `json:"channel"` // "whatsapp", "discord", etc.
	ChatKey  string            `json:"chat"`    // sidecar-stable chat identifier
	Thread   string            `json:"thread"`  // optional
	From     string            `json:"from"`    // display name for logging
	Text     string            `json:"text"`    // user text
	Images   []InboundImageB64 `json:"images"`  // optional attached images
	IsDirect bool              `json:"is_direct"`
}

// InboundImageB64 is one image payload; data is standard base64.
// Empty mime is sniffed from bytes.
type InboundImageB64 struct {
	Mime string `json:"mime"`
	Data string `json:"data"`
}

// OutboundFrame is one event the sidecar receives over SSE.
//
//	kind = "post"   first frame for a turn; sidecar should send a new message
//	kind = "update" replace the message text in place (or post a new chunk)
//	kind = "final"  last frame; close out the message
//	kind = "error"  surface this as an error in-channel
type OutboundFrame struct {
	Channel string `json:"channel"`
	ChatKey string `json:"chat"`
	Thread  string `json:"thread,omitempty"`
	Handle  string `json:"handle"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
}

// Suspender is satisfied by agent.SuspendedApprover and resolves a parked
// approval goroutine via the gateway when the user replies over chat.
type Suspender interface {
	ResumeApproval(callID string, allow bool, approverID string) error
}

// Bridge implements gateway.Channel and serves the HTTP endpoints.
type Bridge struct {
	Dispatcher *gateway.Dispatcher
	Token      string // shared secret in Authorization: Bearer *** empty disables auth
	Listen     string // "127.0.0.1:7799" — binds loopback by default for safety
	Logger     *log.Logger

	mu        sync.Mutex
	subs      map[string]map[int64]*subscriber // channel -> subscriber id -> subscriber
	subSeq    atomic.Int64
	handleSq  atomic.Int64
	server    *http.Server
	suspender Suspender
	running   atomic.Bool
	// Configure before Start. Accepted work has no unbounded ingress queue.
	MaxConcurrentInbound int
	WorkTimeout          time.Duration
	workMu               sync.Mutex
	accepting            bool
	workCtx              context.Context
	workCancel           context.CancelFunc
	workSlots            chan struct{}
	controlSlots         chan struct{}
	workWG               sync.WaitGroup
}

// subscriber holds one SSE subscriber's channel and a done signal.
// done is closed by unsubscribe to safely signal emit to stop sending.
type subscriber struct {
	ch   chan OutboundFrame
	done chan struct{}
}

// New returns a Bridge ready to register on a Hub.
func New(d *gateway.Dispatcher, token, listen string) *Bridge {
	if d != nil {
		d.RejectBusy = true
	}
	return &Bridge{MaxConcurrentInbound: 16, WorkTimeout: 5 * time.Minute,
		Dispatcher: d,
		Token:      token,
		Listen:     listen,
		Logger:     log.New(io.Discard, "", 0),
		subs:       map[string]map[int64]*subscriber{},
	}
}

// SetSuspender injects an approval resolver so inbound "approve/deny <callID>"
// messages are routed to the parked agent goroutine instead of forwarded to
// the dispatcher.
func (b *Bridge) SetSuspender(s Suspender) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.suspender = s
}

// Name implements gateway.Channel.
func (b *Bridge) Name() string { return "bridge" }

// Healthy implements gateway.HealthChecker — returns true while the
// HTTP server is running and accepting connections.
func (b *Bridge) Healthy() bool { return b.running.Load() }

// Ensure compile-time interface compliance.
var _ gateway.HealthChecker = (*Bridge)(nil)

// Run starts the HTTP server and blocks until ctx cancels. Sidecars
// connect over loopback by default — exposing this publicly without a
// reverse proxy is a foot-gun.
func (b *Bridge) Run(ctx context.Context) error {
	done, err := b.Start(ctx)
	if err != nil {
		return err
	}
	return <-done
}

// Addr is the actual bound address after Start, including an OS-chosen port.
func (b *Bridge) Addr() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server == nil {
		return ""
	}
	return "http://" + b.server.Addr
}

// Start binds synchronously so callers can report configuration/listener errors.
// Existing gateway callers may continue using blocking Run.
func (b *Bridge) Start(ctx context.Context) (<-chan error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !b.running.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("bridge: already running")
	}
	failed := true
	defer func() {
		if failed {
			b.running.Store(false)
		}
	}()
	addr := b.Listen
	if addr == "" {
		addr = "127.0.0.1:7799"
	}
	if b.Token == "" && !bindsLoopback(addr) {
		return nil, fmt.Errorf("bridge: refusing to start on %s with empty token — set Token or bind to 127.0.0.1/::1", addr)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bridge: listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/in", b.handleIn)
	mux.HandleFunc("/v1/out", b.handleOut)
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	server := &http.Server{Addr: listener.Addr().String(), Handler: mux, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	b.mu.Lock()
	b.server = server
	b.mu.Unlock()
	b.workMu.Lock()
	limit := b.MaxConcurrentInbound
	if limit <= 0 {
		limit = 16
	}
	b.workCtx, b.workCancel = context.WithCancel(ctx)
	b.workSlots = make(chan struct{}, limit)
	b.controlSlots = make(chan struct{}, 4)
	b.accepting = true
	b.workMu.Unlock()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	done := make(chan error, 1)
	go func() {
		var outcome error
		select {
		case <-ctx.Done():
			b.stopInbound()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := server.Shutdown(shutdown); err != nil {
				_ = server.Close()
			}
			cancel()
			<-serveErr
			if err := b.waitInbound(); err != nil {
				outcome = err
			} else {
				outcome = ctx.Err()
			}
		case err := <-serveErr:
			b.stopInbound()
			_ = server.Close()
			if workErr := b.waitInbound(); workErr != nil {
				err = workErr
			}
			if err == http.ErrServerClosed {
				err = nil
			}
			outcome = err
		}
		b.running.Store(false)
		done <- outcome
		close(done)
	}()

	failed = false
	b.Logger.Printf("bridge: listening on %s", listener.Addr())
	return done, nil
}

func (b *Bridge) authorized(r *http.Request) bool {
	if b.Token == "" {
		return true
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	presented := strings.TrimPrefix(h, "Bearer ")
	return subtle.ConstantTimeCompare([]byte(presented), []byte(b.Token)) == 1
}

// bindsLoopback reports whether addr binds exclusively to a
// loopback interface. Empty host or "0.0.0.0"/"[::]"/no-host
// counts as non-loopback (binds all interfaces).
func bindsLoopback(addr string) bool {
	// addr is "host:port"; SplitHostPort handles both ipv4 and
	// bracketed ipv6.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port → assume binds-all; refuse.
		return false
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// handleIn accepts an inbound message and dispatches it. We do NOT
// stream the reply back over this same response — the sidecar has the
// SSE subscription open separately.
func (b *Bridge) handleIn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !b.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var p InboundPayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if p.Channel == "" || p.ChatKey == "" || (p.Text == "" && len(p.Images) == 0) {
		http.Error(w, "channel, chat, and text or images required", http.StatusBadRequest)
		return
	}

	if b.handleApprovalCommand(w, p) {
		return
	}

	in := gateway.Inbound{
		Channel:  "bridge:" + p.Channel,
		ChatKey:  p.ChatKey,
		Thread:   p.Thread,
		From:     p.From,
		Text:     p.Text,
		IsDirect: p.IsDirect,
	}
	for i, img := range p.Images {
		raw, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			http.Error(w, fmt.Sprintf("image %d: bad base64: %s", i, err), http.StatusBadRequest)
			return
		}
		mime := img.Mime
		if mime == "" {
			mime = vision.MIMEFromBytes(raw)
		}
		in.Images = append(in.Images, gateway.InboundImage{Bytes: raw, Mime: mime})
	}
	reply := &bridgeReply{
		bridge:  b,
		channel: in.Channel,
		chatKey: in.ChatKey,
		thread:  in.Thread,
	}
	if b.Dispatcher == nil {
		http.Error(w, "dispatcher unavailable", http.StatusServiceUnavailable)
		return
	}
	// Cancellation/control commands have their own finite reserve so a full
	// set of held turns cannot prevent /stop from being admitted.
	command := strings.Fields(strings.TrimSpace(p.Text))
	control := len(command) > 0 && (command[0] == "/stop" || command[0] == "/estop")
	if !strings.HasPrefix(strings.TrimSpace(p.Text), "/") && b.Dispatcher.SessionActive(in) {
		http.Error(w, "session busy; retry after its current turn", http.StatusTooManyRequests)
		return
	}
	work, finish, status := b.admitInbound(control)
	if status != 0 {
		http.Error(w, "bridge busy or shutting down; retry later", status)
		return
	}
	go func() {
		defer finish()
		defer func() {
			if panicValue := recover(); panicValue != nil {
				b.Logger.Printf("bridge: inbound panic: %v\n%s", panicValue, debug.Stack())
				_ = reply.Error(work, "", fmt.Errorf("bridge dispatch failed"))
			}
		}()
		b.Dispatcher.Handle(work, in, reply)
	}()
	w.WriteHeader(http.StatusAccepted)
}

func (b *Bridge) admitInbound(control bool) (context.Context, func(), int) {
	b.workMu.Lock()
	defer b.workMu.Unlock()
	if !b.accepting || b.workCtx == nil || b.workCtx.Err() != nil {
		return nil, nil, http.StatusServiceUnavailable
	}
	slots := b.workSlots
	if control {
		slots = b.controlSlots
	}
	select {
	case slots <- struct{}{}:
	default:
		return nil, nil, http.StatusTooManyRequests
	}
	timeout := b.WorkTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(b.workCtx, timeout)
	b.workWG.Add(1)
	return ctx, func() { cancel(); <-slots; b.workWG.Done() }, 0
}
func (b *Bridge) stopInbound() {
	b.workMu.Lock()
	b.accepting = false
	if b.workCancel != nil {
		b.workCancel()
	}
	b.workMu.Unlock()
}
func (b *Bridge) waitInbound() error {
	done := make(chan struct{})
	go func() { b.workWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("bridge: accepted work did not stop after cancellation")
	}
}

// handleOut opens an SSE stream for one channel name. Sidecars that
// adapt multiple platforms can either filter client-side or run one
// subscription per channel.
func (b *Bridge) handleOut(w http.ResponseWriter, r *http.Request) {
	if !b.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		http.Error(w, "channel query param required", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id, ch := b.subscribe("bridge:" + channel)
	// Retrieve the subscriber's done channel so the SSE loop can
	// detect unsubscribe (done is closed when the sidecar disconnects).
	b.mu.Lock()
	var doneCh chan struct{}
	if subs, ok := b.subs["bridge:"+channel]; ok {
		if s, ok := subs[id]; ok {
			doneCh = s.done
		}
	}
	b.mu.Unlock()
	defer b.unsubscribe("bridge:"+channel, id)

	// Flush headers immediately so the client's HTTP read returns and it
	// can start consuming frames. Without this, http.Client.Do blocks
	// until the first event ever arrives.
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-doneCh:
			return
		case frame := <-ch:
			fmt.Fprint(w, "data: ")
			_ = enc.Encode(frame)
			fmt.Fprint(w, "\n")
			flusher.Flush()
		}
	}
}

func (b *Bridge) subscribe(channel string) (int64, chan OutboundFrame) {
	id := b.subSeq.Add(1)
	ch := make(chan OutboundFrame, 32)
	s := &subscriber{ch: ch, done: make(chan struct{})}
	b.mu.Lock()
	if _, ok := b.subs[channel]; !ok {
		b.subs[channel] = map[int64]*subscriber{}
	}
	b.subs[channel][id] = s
	b.mu.Unlock()
	return id, ch
}

func (b *Bridge) unsubscribe(channel string, id int64) {
	b.mu.Lock()
	if subs, ok := b.subs[channel]; ok {
		if s, ok := subs[id]; ok {
			delete(subs, id)
			// Close done to signal the SSE reader to exit, but
			// do NOT close s.ch — emit may still hold a reference
			// and would panic on send-to-closed-channel.
			close(s.done)
		}
	}
	b.mu.Unlock()
}

// emit fans a frame out to every subscriber for the channel. Slow
// subscribers drop frames rather than blocking the agent loop.
// The done channel signals that a subscriber has been unsubscribed;
// ch is never closed by us, so sends on it never panic.
func (b *Bridge) emit(frame OutboundFrame) {
	b.mu.Lock()
	subs := b.subs[frame.Channel]
	targets := make([]*subscriber, 0, len(subs))
	for _, s := range subs {
		targets = append(targets, s)
	}
	b.mu.Unlock()
	for _, s := range targets {
		select {
		case <-s.done:
			// subscriber was unsubscribed; skip
			continue
		default:
		}
		select {
		case s.ch <- frame:
		default:
			b.Logger.Printf("bridge: drop frame for %s (subscriber slow)", frame.Channel)
		}
	}
}

// bridgeReply is the gateway.Reply implementation backed by SSE.
type bridgeReply struct {
	bridge  *Bridge
	channel string
	chatKey string
	thread  string
}

func (r *bridgeReply) newHandle() string {
	return strconv.FormatInt(r.bridge.handleSq.Add(1), 10)
}

func (r *bridgeReply) emit(handle, kind, text string) {
	r.bridge.emit(OutboundFrame{
		Channel: r.channel, ChatKey: r.chatKey, Thread: r.thread,
		Handle: handle, Kind: kind, Text: text,
	})
}

func (r *bridgeReply) PostInitial(_ context.Context, _ gateway.Inbound, text string) (string, error) {
	h := r.newHandle()
	r.emit(h, "post", text)
	return h, nil
}

func (r *bridgeReply) Update(_ context.Context, handle, text string) error {
	r.emit(handle, "update", text)
	return nil
}

func (r *bridgeReply) Final(_ context.Context, handle, text string) error {
	r.emit(handle, "final", text)
	return nil
}

func (r *bridgeReply) Error(_ context.Context, handle string, err error) error {
	r.emit(handle, "error", err.Error())
	return nil
}

func (r *bridgeReply) StartTyping() (stop func()) { return func() {} }

var approvalCmdRe = regexp.MustCompile(`^(approve|deny) ([a-f0-9-]+)$`)

// handleApprovalCommand checks whether the inbound text is an approval command
// ("approve <callID>" or "deny <callID>"). When a Suspender is wired and the
// text matches, it resolves the pending approval and writes an SSE reply
// instead of forwarding to the agent dispatcher. Returns true when consumed.
func (b *Bridge) handleApprovalCommand(w http.ResponseWriter, p InboundPayload) bool {
	b.mu.Lock()
	s := b.suspender
	b.mu.Unlock()

	if s == nil {
		return false
	}

	m := approvalCmdRe.FindStringSubmatch(strings.TrimSpace(p.Text))
	if m == nil {
		return false
	}

	action, callID := m[1], m[2]
	allow := action == "approve"

	from := p.From
	if from == "" {
		from = p.ChatKey
	}

	var replyText string
	if err := s.ResumeApproval(callID, allow, from); err != nil {
		replyText = fmt.Sprintf("error: %s", err.Error())
	} else if allow {
		replyText = fmt.Sprintf("approved %s", callID)
	} else {
		replyText = fmt.Sprintf("denied %s", callID)
	}

	b.emit(OutboundFrame{
		Channel: "bridge:" + p.Channel,
		ChatKey: p.ChatKey,
		Thread:  p.Thread,
		Handle:  strconv.FormatInt(b.handleSq.Add(1), 10),
		Kind:    "final",
		Text:    replyText,
	})

	w.WriteHeader(http.StatusAccepted)
	return true
}
