// Package web serves the Ethos browser UI: an embedded SPA over HTTP plus a
// WebSocket event channel for live agent streams. Same agent backend as the
// TUI; reads from the same session store.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
)

// AgentSender is the slice of agent.Agent the web server actually needs.
// Defining it locally keeps the package mockable in tests.
type AgentSender interface {
	Stream(ctx context.Context, userInput string) (<-chan agent.StreamEvent, error)
	Model() string
	SessionID() string
	SetSessionID(id string)
}

// Config drives Server construction. Token is required unless NoAuth is true
// (only meaningful with a localhost listen address).
type Config struct {
	Addr     string
	Token    string
	NoAuth   bool
	Agent    AgentSender        // required for /api/send to work
	Store    session.Store      // optional; persistence
	Catalog  *providers.Catalog // optional; powers the model picker
	Provider string
	Version  string
}

// Server is the HTTP front for the web UI.
type Server struct {
	cfg     Config
	httpSrv *http.Server

	bus      *eventBus
	mu       sync.Mutex
	streams  map[string]context.CancelFunc // sessionID -> cancel of in-flight stream
}

// NewServer wires the routes; Start binds the listener.
func NewServer(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8420"
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	return &Server{
		cfg:     cfg,
		bus:     newEventBus(),
		streams: make(map[string]context.CancelFunc),
	}
}

// Handler returns the http.Handler so tests can drive it without a TCP socket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static SPA — served from embedded FS so the binary is self-contained.
	sub, err := fs.Sub(Assets, "static")
	if err != nil {
		// Embed declared above; impossible at runtime unless someone deleted
		// the directive. Fail loudly.
		panic(fmt.Errorf("web: missing static fs: %w", err))
	}
	staticHandler := http.StripPrefix("/static/", cacheStatic(http.FileServer(http.FS(sub))))
	mux.Handle("/static/", staticHandler)
	mux.HandleFunc("/", s.handleIndex(sub))

	// API
	mux.HandleFunc("/api/info", s.auth(s.handleInfo))
	mux.HandleFunc("/api/sessions", s.auth(s.handleSessions))
	mux.HandleFunc("/api/sessions/", s.auth(s.handleSessionSub))
	mux.HandleFunc("/api/send", s.auth(s.handleSend))
	mux.HandleFunc("/api/cancel", s.auth(s.handleCancel))
	mux.HandleFunc("/api/models", s.auth(s.handleModels))
	mux.HandleFunc("/api/events", s.auth(s.handleEvents))
	return mux
}

// Start binds and serves. Non-blocking: returns once the listener is up.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("web: listen %s: %w", s.cfg.Addr, err)
	}
	s.cfg.Addr = ln.Addr().String()
	s.httpSrv = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = s.httpSrv.Serve(ln) }()
	return nil
}

// Shutdown stops the HTTP server and closes any active streams.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	for _, cancel := range s.streams {
		cancel()
	}
	s.streams = make(map[string]context.CancelFunc)
	s.mu.Unlock()
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Shutdown(ctx)
}

// Addr is the resolved listen address (useful when ":0" was requested).
func (s *Server) Addr() string { return s.cfg.Addr }

// Token returns the configured bearer token (empty when --no-auth).
func (s *Server) Token() string { return s.cfg.Token }

// ----- middleware -------------------------------------------------------

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.NoAuth || s.cfg.Token == "" {
			h(w, r); return
		}
		if tokenFromRequest(r) == s.cfg.Token {
			h(w, r); return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

func tokenFromRequest(r *http.Request) string {
	if t := r.URL.Query().Get("t"); t != "" {
		return t
	}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		return strings.TrimPrefix(a, "Bearer ")
	}
	if c, err := r.Cookie("ethos-token"); err == nil {
		return c.Value
	}
	return ""
}

func cacheStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		h.ServeHTTP(w, r)
	})
}

// ----- index -------------------------------------------------------------

func (s *Server) handleIndex(sub fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r); return
		}
		// First-load token plumbing: ?t=… sets the cookie before app.js runs.
		if t := r.URL.Query().Get("t"); t != "" {
			http.SetCookie(w, &http.Cookie{
				Name: "ethos-token", Value: t, Path: "/",
				MaxAge: 60 * 60 * 24 * 365, SameSite: http.SameSiteLaxMode,
			})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		f, err := sub.Open("index.html")
		if err != nil { http.Error(w, "missing index", 500); return }
		defer f.Close()
		_, _ = io.Copy(w, f)
	}
}

// ----- /api/info ---------------------------------------------------------

type infoResponse struct {
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	Version      string   `json:"version"`
	SessionID    string   `json:"sessionId"`
	Capabilities []string `json:"capabilities"`
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	out := infoResponse{
		Provider: s.cfg.Provider,
		Version:  s.cfg.Version,
		Capabilities: []string{"send", "stream", "cancel", "sessions", "models"},
	}
	if s.cfg.Agent != nil {
		out.Model = s.cfg.Agent.Model()
		out.SessionID = s.cfg.Agent.SessionID()
	}
	writeJSON(w, http.StatusOK, out)
}

// ----- /api/sessions -----------------------------------------------------

type sessionRow struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	Model        string    `json:"model"`
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil {
		writeJSON(w, http.StatusOK, []sessionRow{})
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.cfg.Store.List(r.Context(), session.ListOptions{Limit: 200})
		if err != nil {
			http.Error(w, err.Error(), 500); return
		}
		out := make([]sessionRow, 0, len(list))
		for _, s := range list {
			out = append(out, sessionRow{
				ID: s.ID, Title: s.Title, UpdatedAt: s.UpdatedAt,
				MessageCount: len(s.Messages), Model: s.Model,
			})
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var body struct{ Title, Folder string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		ns := session.NewSession(body.Folder)
		ns.Title = body.Title
		if err := s.cfg.Store.Create(r.Context(), ns); err != nil {
			http.Error(w, err.Error(), 500); return
		}
		writeJSON(w, http.StatusCreated, ns)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSessionSub(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Store == nil { http.NotFound(w, r); return }
	rest := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" { http.NotFound(w, r); return }
	id := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			sess, err := s.cfg.Store.Load(r.Context(), id)
			if err != nil { http.Error(w, err.Error(), 404); return }
			writeJSON(w, http.StatusOK, sess)
		case http.MethodDelete:
			if err := s.cfg.Store.Delete(r.Context(), id); err != nil {
				http.Error(w, err.Error(), 500); return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if parts[1] == "rename" && r.Method == http.MethodPost {
		var body struct{ Title string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		sess, err := s.cfg.Store.Load(r.Context(), id)
		if err != nil { http.Error(w, err.Error(), 404); return }
		sess.Title = body.Title
		if err := s.cfg.Store.Save(r.Context(), sess); err != nil {
			http.Error(w, err.Error(), 500); return
		}
		writeJSON(w, http.StatusOK, sess); return
	}
	http.NotFound(w, r)
}

// ----- /api/send + /api/cancel ------------------------------------------

type sendRequest struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}

type sendResponse struct {
	MessageID string `json:"messageId"`
	SessionID string `json:"sessionId"`
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "POST only", 405); return }
	if s.cfg.Agent == nil { http.Error(w, "no agent", 503); return }
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", 400); return
	}
	if req.Text == "" { http.Error(w, "text required", 400); return }

	sid := req.SessionID
	if sid == "" { sid = s.cfg.Agent.SessionID() }
	if sid == "" { sid = "default" }
	// Best-effort: align the agent's session id with the one the browser
	// asked for so persisted history lands in the right bucket.
	s.cfg.Agent.SetSessionID(sid)

	msgID := uuid.New().String()

	// Cancel any in-flight stream for this session before launching a new one.
	s.mu.Lock()
	if cancel, ok := s.streams[sid]; ok { cancel() }
	ctx, cancel := context.WithCancel(context.Background())
	s.streams[sid] = cancel
	s.mu.Unlock()

	go s.runStream(ctx, sid, msgID, req.Text)

	writeJSON(w, http.StatusAccepted, sendResponse{MessageID: msgID, SessionID: sid})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	var body struct{ SessionID string `json:"sessionId"` }
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.mu.Lock()
	if cancel, ok := s.streams[body.SessionID]; ok {
		cancel(); delete(s.streams, body.SessionID)
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) runStream(ctx context.Context, sid, msgID, text string) {
	defer func() {
		s.mu.Lock(); delete(s.streams, sid); s.mu.Unlock()
	}()
	ch, err := s.cfg.Agent.Stream(ctx, text)
	if err != nil {
		s.bus.publish(sid, wsEvent{Type: "error", SessionID: sid, MessageID: msgID, Error: err.Error()})
		return
	}
	for ev := range ch {
		out := wsEvent{SessionID: sid, MessageID: msgID, Timestamp: time.Now().UTC()}
		switch ev.Type {
		case agent.EventToken:
			out.Type = "text_delta"
			out.Content = ev.Content
		case agent.EventToolStart:
			out.Type = "tool_start"
			if ev.ToolCall != nil {
				out.ToolName = ev.ToolCall.Name
				out.ToolArgs = ev.ToolCall.Arguments
			}
		case agent.EventToolOutput:
			out.Type = "tool_output"
			if ev.ToolCall != nil { out.ToolName = ev.ToolCall.Name }
			out.Content = ev.Content
		case agent.EventDone:
			out.Type = "done"
		case agent.EventError:
			out.Type = "error"
			if ev.Error != nil { out.Error = ev.Error.Error() }
		default:
			continue
		}
		s.bus.publish(sid, out)
	}
}

// ----- /api/models -------------------------------------------------------

type modelRow struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Name     string `json:"name,omitempty"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Catalog == nil {
		writeJSON(w, http.StatusOK, []modelRow{}); return
	}
	flat := s.cfg.Catalog.All()
	out := make([]modelRow, 0, len(flat))
	for _, m := range flat {
		out = append(out, modelRow{ID: m.Model.ID, Provider: m.ProviderID, Name: m.Model.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

// ----- /api/events (WebSocket) -------------------------------------------

type wsEvent struct {
	Type      string    `json:"type"`
	SessionID string    `json:"sessionId,omitempty"`
	MessageID string    `json:"messageId,omitempty"`
	Content   string    `json:"content,omitempty"`
	ToolName  string    `json:"toolName,omitempty"`
	ToolArgs  string    `json:"toolArgs,omitempty"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	conn, err := upgradeWS(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest); return
	}
	defer conn.Close()

	sub := s.bus.subscribe()
	defer s.bus.unsubscribe(sub)

	// Reader goroutine drains client frames + handles pings.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_ = conn.ReadLoop()
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-readDone:
			return
		case <-ping.C:
			if err := conn.WritePing(); err != nil { return }
		case ev, ok := <-sub.ch:
			if !ok { return }
			data, err := json.Marshal(ev)
			if err != nil { continue }
			if err := conn.WriteText(data); err != nil { return }
		}
	}
}

// ----- helpers -----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
