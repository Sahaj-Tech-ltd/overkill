package acp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// AgentEventType enumerates the event categories the ACP server cares about.
// Defining a local enum keeps acp from importing internal/agent (which would
// create an import cycle: tools → acp → agent → tools).
type AgentEventType int

const (
	AgentEventToken AgentEventType = iota
	AgentEventToolStart
	AgentEventToolOutput
	AgentEventDone
	AgentEventError
)

// AgentEvent is the wire-friendly StreamEvent the Sender hands to the server.
type AgentEvent struct {
	Type     AgentEventType
	Content  string
	ToolName string
	ToolArgs string
	Error    error
}

// Sender is the slice of agent.Agent that the ACP server actually needs.
// Defining it locally keeps the package free of circular imports.
type Sender interface {
	StreamACP(ctx context.Context, userInput string) (<-chan AgentEvent, error)
	Model() string
	SessionID() string
}

// Server exposes the ACP HTTP+SSE surface.
type Server struct {
	mu             sync.Mutex
	addr           string
	token          string
	allowedOrigins []string
	agent          Sender
	store          session.Store
	streams        map[string]*messageStream
	inbound        []InboundLog // ring buffer of recent inbound messages for /acp dialog
	inboundMax     int
	httpSrv        *http.Server
	name           string
	version        string
	// ctx is the server's lifetime context. Per-message agent runs
	// derive from this so Shutdown cancels in-flight work and the
	// streams map gets drained instead of leaking stalled runs.
	// Set by Run; nil before then.
	ctx       context.Context
	ctxCancel context.CancelFunc
	// jobStore and jobWorker are optional; nil disables /v1/jobs endpoints.
	jobStore  *daemon.JobStore
	jobWorker *daemon.Worker
	// routes records every registered route pattern. Populated by Handler()
	// and exposed via Routes() so the auth guard test can auto-discover all
	// routes without a human-maintained list. §8.7.5 machine-checked auth guard.
	routes []RoutePattern
}

// RoutePattern is one registered route with its HTTP method expectation.
type RoutePattern struct {
	Path    string   // e.g. "/v1/info", "/v1/messages/{id}/events"
	Methods []string // expected methods; empty = test all standard methods
}

// InboundLog records that a peer sent us a message; the /acp dialog displays
// the last few entries.
type InboundLog struct {
	From      string    `json:"from"`
	MessageID string    `json:"messageID"`
	Snippet   string    `json:"snippet"`
	At        time.Time `json:"at"`
}

type messageStream struct {
	id     string
	events chan Event
	cancel context.CancelFunc
	closed bool
	mu     sync.Mutex
}

// Config is the constructor input.
type Config struct {
	Addr           string
	Token          string
	AllowedOrigins []string
	Agent          Sender
	Store          session.Store
	Name           string
	Version        string
	// JobStore and JobWorker are optional. When both are non-nil the server
	// registers the /v1/jobs endpoints and wires job submission through the worker.
	JobStore  *daemon.JobStore
	JobWorker *daemon.Worker
}

// GenerateToken returns a fresh 32-byte hex token.
func GenerateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func NewServer(cfg Config) *Server {
	addr := cfg.Addr
	if addr == "" {
		addr = "127.0.0.1:8421"
	}
	allowed := cfg.AllowedOrigins
	if len(allowed) == 0 {
		allowed = []string{"http://localhost", "http://127.0.0.1"}
	}
	name := cfg.Name
	if name == "" {
		name = "overkill"
	}
	version := cfg.Version
	if version == "" {
		version = "dev"
	}
	return &Server{
		addr:           addr,
		token:          cfg.Token,
		allowedOrigins: allowed,
		agent:          cfg.Agent,
		store:          cfg.Store,
		streams:        make(map[string]*messageStream),
		inboundMax:     32,
		name:           name,
		version:        version,
		jobStore:       cfg.JobStore,
		jobWorker:      cfg.JobWorker,
	}
}

// Token exposes the bearer token for clients (read by /acp dialog and
// `overkill acp token`).
func (s *Server) Token() string { return s.token }

// Addr returns the server's listen address.
func (s *Server) Addr() string { return s.addr }

// RecentInbound is a snapshot of the inbound ring buffer.
func (s *Server) RecentInbound() []InboundLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]InboundLog, len(s.inbound))
	copy(out, s.inbound)
	return out
}

// Handler returns the http.Handler so callers can mount it under their own mux
// (and so tests can drive it without a TCP listener).
// Also populates s.routes so the auth guard test can auto-discover all routes.
// Safe for concurrent use.
func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()

	mux := http.NewServeMux()
	s.routes = nil // reset on each call

	register := func(pattern string, handler http.HandlerFunc, methods ...string) {
		mux.HandleFunc(pattern, handler)
		s.routes = append(s.routes, RoutePattern{Path: pattern, Methods: methods})
	}

	register("/v1/info", s.withAuth(s.handleInfo), http.MethodGet)
	register("/v1/messages", s.withAuth(s.handleMessages), http.MethodPost)
	register("/v1/messages/", s.withAuth(s.handleMessageSub), http.MethodGet, http.MethodPost)
	register("/v1/sessions", s.withAuth(s.handleSessions), http.MethodGet, http.MethodPost)
	register("/v1/sessions/", s.withAuth(s.handleSessionSub), http.MethodGet)
	if s.jobStore != nil {
		register("/v1/jobs", s.withAuth(s.handleJobs), http.MethodGet, http.MethodPost)
		register("/v1/jobs/", s.withAuth(s.handleJobSub), http.MethodGet)
	}
	return s.cors(mux)
}

// Routes returns every registered route pattern. The auth guard test (§8.7.5)
// uses this to auto-discover all routes and assert 401 on each, so a new route
// added without auth fails the test automatically.
func (s *Server) Routes() []RoutePattern {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.routes
}

// Start binds the server and runs http.Serve in a goroutine. Use Shutdown to
// stop it.
func (s *Server) Start() error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.httpSrv = srv
	s.ctx, s.ctxCancel = context.WithCancel(context.Background())
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("acp: ListenAndServe error: %v", err)
		}
	}()
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	// Cancel in-flight agent runs first so the HTTP shutdown doesn't
	// have to wait on background streams. Each stream's send loop
	// observes the parent ctx cancel and drains.
	if s.ctxCancel != nil {
		s.ctxCancel()
	}
	return s.httpSrv.Shutdown(ctx)
}

// ----- middleware --------------------------------------------------------

func (s *Server) withAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			writeErrorJSON(w, http.StatusInternalServerError, "acp server misconfigured: no auth token")
			return
		}
		got := r.Header.Get("Authorization")
		if !strings.HasPrefix(got, "Bearer ") {
			writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// Constant-time compare so an attacker can't recover the
		// token byte-by-byte via response-time differences.
		presented := strings.TrimPrefix(got, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(presented), []byte(s.token)) != 1 {
			writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	}
}

func (s *Server) cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// originAllowed compares the request origin against the allow-list
// EXACTLY — scheme + host + port must match. Prior code used
// strings.HasPrefix which let `http://localhost.evil.com` match the
// allow-listed `http://localhost`. Combined with
// Access-Control-Allow-Credentials: true that was a real CSRF /
// token-exfil surface.
func (s *Server) originAllowed(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	canonical := parsed.Scheme + "://" + parsed.Host
	for _, a := range s.allowedOrigins {
		if canonical == a || origin == a {
			return true
		}
	}
	return false
}

// ----- handlers ----------------------------------------------------------

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	model := ""
	if s.agent != nil {
		model = s.agent.Model()
	}
	writeJSON(w, http.StatusOK, Info{
		Name:    s.name,
		Version: s.version,
		Model:   model,
		Capabilities: []string{
			"messages.send", "messages.stream", "messages.cancel",
			"sessions.list", "sessions.read",
		},
	})
}

// maxACPBody caps POST /v1/messages request bodies at 1 MiB,
// matching the web/server.go limitBody middleware. Prevents a
// multi-GB JSON payload from OOMing the process during decode.
const maxACPBody = 1 << 20

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxACPBody)
	var req SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.Content == "" {
		writeErrorJSON(w, http.StatusBadRequest, "content required")
		return
	}
	if s.agent == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "no agent attached")
		return
	}

	msgID := uuid.New().String()
	// Use the server's lifetime context so a server Shutdown cancels
	// the run, but DON'T inherit r.Context() — the HTTP handler
	// returns 202 immediately, after which r.Context() is cancelled
	// by the http stack. The agent run intentionally outlives that.
	// HTTP client cancellation flows through the explicit
	// /v1/messages/{id}/cancel endpoint instead, which calls
	// stream.cancel().
	parentCtx := s.ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(parentCtx)

	stream := &messageStream{
		id:     msgID,
		events: make(chan Event, 64),
		cancel: cancel,
	}
	s.mu.Lock()
	s.streams[msgID] = stream
	if len(s.inbound) >= s.inboundMax {
		s.inbound = s.inbound[1:]
	}
	snippet := req.Content
	if len(snippet) > 80 {
		snippet = snippet[:80] + "..."
	}
	s.inbound = append(s.inbound, InboundLog{
		From: req.From, MessageID: msgID, Snippet: snippet, At: time.Now().UTC(),
	})
	s.mu.Unlock()

	// Run the agent in a goroutine; pump AgentEvent into our channel.
	go func() {
		defer func() {
			stream.close()
			// Drop the streams-map entry so the buffered channel +
			// stream struct can be GC'd. Without this the map grew
			// O(messages ever seen) — a long-running bot bot accumulated
			// thousands of completed streams. Drain after a brief
			// grace window so an SSE subscriber that opened the
			// /events endpoint after send completes still has a
			// chance to drain the buffer.
			go func() {
				time.Sleep(60 * time.Second)
				s.mu.Lock()
				delete(s.streams, msgID)
				s.mu.Unlock()
			}()
		}()
		ch, err := s.agent.StreamACP(ctx, req.Content)
		if err != nil {
			stream.send(Event{Type: "error", Error: err.Error(), Timestamp: time.Now().UTC()})
			return
		}
		for ev := range ch {
			out := Event{Timestamp: time.Now().UTC()}
			switch ev.Type {
			case AgentEventToken:
				out.Type = "text_delta"
				out.Content = ev.Content
			case AgentEventToolStart, AgentEventToolOutput:
				out.Type = "tool_call"
				out.ToolName = ev.ToolName
				out.ToolArgs = ev.ToolArgs
				out.Content = ev.Content
			case AgentEventDone:
				out.Type = "done"
			case AgentEventError:
				out.Type = "error"
				if ev.Error != nil {
					out.Error = ev.Error.Error()
				}
			default:
				continue
			}
			stream.send(out)
		}
	}()

	sid := req.SessionID
	if sid == "" && s.agent != nil {
		sid = s.agent.SessionID()
	}
	writeJSON(w, http.StatusAccepted, SendResponse{MessageID: msgID, SessionID: sid})
}

// AC3/AC4 — handleMessageSub returns JSON errors for unknown streams + bad paths.
func (s *Server) handleMessageSub(w http.ResponseWriter, r *http.Request) {
	// /v1/messages/{id}/events  or  /v1/messages/{id}/cancel
	rest := strings.TrimPrefix(r.URL.Path, "/v1/messages/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	id, sub := parts[0], parts[1]
	s.mu.Lock()
	stream, ok := s.streams[id]
	s.mu.Unlock()
	if !ok {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	switch sub {
	case "events":
		s.serveSSE(w, r, stream)
	case "cancel":
		stream.cancel()
		stream.close()
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
	default:
		writeErrorJSON(w, http.StatusNotFound, "not found")
	}
}

// sseTimeout caps per-SSE-connection lifetime at 5 minutes, preventing
// abandoned connections from leaking goroutines indefinitely. A new SSE
// connection can always be opened if the client needs to resume.
const sseTimeout = 5 * time.Minute

func (s *Server) serveSSE(w http.ResponseWriter, r *http.Request, stream *messageStream) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrorJSON(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	timer := time.NewTimer(sseTimeout)
	defer timer.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			return
		case ev, ok := <-stream.events:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
			flusher.Flush()
			if ev.Type == "done" || ev.Type == "error" {
				return
			}
		}
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	switch r.Method {
	case http.MethodGet:
		sessions, err := s.store.List(r.Context(), session.ListOptions{Limit: 100})
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sessions)
	case http.MethodPost:
		var body struct {
			Folder string `json:"folder"`
			Title  string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErrorJSON(w, http.StatusBadRequest, "bad request: "+err.Error())
			return
		}
		ns := session.NewSession(body.Folder)
		ns.Title = body.Title
		if err := s.store.Create(r.Context(), ns); err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, ns)
	default:
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleSessionSub(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	id = strings.Trim(id, "/")
	if id == "" {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	sess, err := s.store.Load(r.Context(), id)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// ----- job handlers -------------------------------------------------------

// handleJobs serves POST /v1/jobs and GET /v1/jobs.
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleJobCreate(w, r)
	case http.MethodGet:
		s.handleJobList(w, r)
	default:
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Intent  string `json:"intent"`
		Channel string `json:"channel"`
		ChatKey string `json:"chat_key"`
		Profile string `json:"profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, "bad request")
		return
	}
	if body.Intent == "" {
		writeErrorJSON(w, http.StatusBadRequest, "intent required")
		return
	}
	profile := body.Profile
	if body.Channel != "" && profile == "" {
		profile = "remote"
	}
	job := daemon.NewJob(body.Intent, body.Channel, body.ChatKey, profile)
	ctx := r.Context()
	if err := s.jobStore.Create(ctx, job); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.jobWorker != nil {
		_ = s.jobWorker.Submit(job)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"job_id": job.ID})
}

func (s *Server) handleJobList(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.jobStore.List(r.Context())
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []daemon.Job{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

// handleJobSub serves GET /v1/jobs/{id} and POST /v1/jobs/{id}/cancel.
func (s *Server) handleJobSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	rest = strings.Trim(rest, "/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	if id == "" {
		writeErrorJSON(w, http.StatusNotFound, "not found")
		return
	}
	ctx := r.Context()
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		job, err := s.jobStore.Get(ctx, id)
		if err != nil {
			writeErrorJSON(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, job)
		return
	}
	if parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := s.jobStore.Cancel(ctx, id); err != nil {
			writeErrorJSON(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
		return
	}
	writeErrorJSON(w, http.StatusNotFound, "not found")
}

// ----- helpers -----------------------------------------------------------

func (m *messageStream) send(ev Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	select {
	case m.events <- ev:
	default:
	}
}

func (m *messageStream) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	close(m.events)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeErrorJSON sends a JSON-shaped error response: {"error":"message"}.
// Replaces http.Error so every error response is machine-parseable and
// consistent across the entire API surface.
func writeErrorJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
