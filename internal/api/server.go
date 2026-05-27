package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// Server is the JSON-RPC 2.0 HTTP server that wraps the existing internal
// packages behind a clean API for the Ink TUI to consume.
type Server struct {
	mu           sync.RWMutex
	cfg          *config.Config
	sessionStore session.Store
	agents       map[string]*agent.Agent // session ID → agent
	toolRegistry *tools.Registry
	httpServer   *http.Server
	port         int // set after Start()
}

// Addr returns the address the server is listening on.
// Only valid after Start() has been called.
func (s *Server) Addr() string {
	return fmt.Sprintf("http://localhost:%d", s.port)
}

// ServerConfig holds everything the API server needs at startup.
type ServerConfig struct {
	Config       *config.Config
	SessionStore session.Store
	Tools        *tools.Registry
}

// NewServer creates a new API server. Call Start to begin listening.
func NewServer(sc ServerConfig) *Server {
	reg := sc.Tools
	if reg == nil {
		reg = tools.NewRegistry()
	}
	return &Server{
		cfg:          sc.Config,
		sessionStore: sc.SessionStore,
		agents:       make(map[string]*agent.Agent),
		toolRegistry: reg,
	}
}

// Start binds to localhost:0 (OS-chosen port), prints the address to stderr,
// and blocks until the context is cancelled or SIGINT/SIGTERM is received.
// Graceful shutdown is handled automatically.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", s.withMiddleware(s.handleRPC))
	mux.HandleFunc("/sse", s.withMiddleware(s.handleSSE))
	mux.HandleFunc("/stream", s.withMiddleware(s.handleStream))
	mux.HandleFunc("/health", s.withMiddleware(s.handleHealth))

	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return fmt.Errorf("api: listen: %w", err)
	}

	addr := ln.Addr().(*net.TCPAddr)
	s.port = addr.Port
	log.Printf("API listening on http://localhost:%d", addr.Port)

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // no timeout — SSE streams may be long-lived
	}

	// Shutdown on context cancellation or OS signal.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		select {
		case sig := <-sigCh:
			log.Printf("received %v, shutting down API server", sig)
		case <-ctx.Done():
			log.Printf("context cancelled, shutting down API server")
		}
		shutdownCancel()
		// Give in-flight requests 5s to finish.
		sdCtx, sdCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer sdCancel()
		_ = s.httpServer.Shutdown(sdCtx)
	}()

	err = s.httpServer.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	// If the shutdown goroutine closed the listener, Serve returns a
	// net.ErrClosed wrapped error. Treat that as clean shutdown too.
	if err != nil {
		select {
		case <-shutdownCtx.Done():
			return nil
		default:
		}
	}
	return err
}

// ---------------------------------------------------------------------------
// HTTP routing
// ---------------------------------------------------------------------------

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCResponse(w, Response{
			JSONRPC: "2.0",
			Error:   &RPCError{Code: ParseError, Message: errorString(ParseError)},
		})
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCResponse(w, Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: InvalidRequest, Message: "jsonrpc must be \"2.0\""},
		})
		return
	}

	ctx := r.Context()

	var resp Response
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	switch req.Method {
	case "agent.send":
		result, rpcErr := s.handleAgentSend(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "agent.abort":
		result, rpcErr := s.handleAgentAbort(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "estop":
		result, rpcErr := s.handleEStop(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "session.list":
		result, rpcErr := s.handleSessionList(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "session.create":
		result, rpcErr := s.handleSessionCreate(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "session.delete":
		result, rpcErr := s.handleSessionDelete(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "config.get":
		result, rpcErr := s.handleConfigGet(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "config.update":
		result, rpcErr := s.handleConfigUpdate(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "providers.list":
		result, rpcErr := s.handleProvidersList(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "models.list":
		result, rpcErr := s.handleModelsList(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "status.health":
		result, rpcErr := s.handleStatusHealth(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	default:
		resp.Error = &RPCError{Code: MethodNotFound, Message: fmt.Sprintf("unknown method: %s", req.Method)}
	}

	writeRPCResponse(w, resp)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		http.Error(w, "session query parameter required", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Allow the client to abort via POST /rpc with agent.abort, but also
	// wire the request context so disconnecting the HTTP connection cancels
	// the agent.
	a, rpcErr := s.getOrCreateAgent(ctx, sessionID)
	if rpcErr != nil {
		http.Error(w, rpcErr.Message, http.StatusInternalServerError)
		return
	}

	events, err := a.Stream(ctx, r.URL.Query().Get("message"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.consumeStreamEvents(ctx, w, flusher, events)

	// Persist after stream completes.
	s.saveSessionState(ctx, sessionID, a)
}

// handleStream is the plan-aligned SSE endpoint at GET /stream.
// Accepts two query-param styles:
//  1. Direct: ?session_id=X&message=...
//  2. JSON-RPC style (TUI client): ?method=agent.send&params={"message":"...","session_id":"..."}
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	message := r.URL.Query().Get("message")

	// Fallback: parse JSON-RPC style params if direct query params are missing.
	if message == "" {
		paramsRaw := r.URL.Query().Get("params")
		if paramsRaw != "" {
			var p struct {
				SessionID string `json:"session_id"`
				Message   string `json:"message"`
			}
			if err := json.Unmarshal([]byte(paramsRaw), &p); err == nil {
				if p.SessionID != "" {
					sessionID = p.SessionID
				}
				message = p.Message
			}
		}
	}

	if sessionID == "" {
		// Auto-create a session for this folder, same as handleAgentSend.
		sess := session.NewSession(getCwd())
		if createErr := s.sessionStore.Create(r.Context(), sess); createErr != nil {
			http.Error(w, fmt.Sprintf("failed to create session: %v", createErr), http.StatusInternalServerError)
			return
		}
		sessionID = sess.ID
	}
	if message == "" {
		http.Error(w, "message query parameter required", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	a, rpcErr := s.getOrCreateAgent(ctx, sessionID)
	if rpcErr != nil {
		http.Error(w, rpcErr.Message, http.StatusInternalServerError)
		return
	}

	events, err := a.Stream(ctx, message)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.consumeStreamEvents(ctx, w, flusher, events)

	// Persist after stream completes.
	s.saveSessionState(ctx, sessionID, a)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HealthResult{
		Status:  "ok",
		Version: s.cfg.Version,
	})
}

// ---------------------------------------------------------------------------
// SSE helpers
// ---------------------------------------------------------------------------

// consumeStreamEvents reads from the agent's event channel and writes SSE
// formatted events. It blocks until the channel closes or ctx is done.
func (s *Server) consumeStreamEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, events <-chan agent.StreamEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			s.writeSSEEvent(w, flusher, evt)
		}
	}
}

func (s *Server) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, evt agent.StreamEvent) {
	sseType := streamEventType(evt)
	data := buildSSEData(evt)

	encoded, err := json.Marshal(data)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", sseType, encoded)
	flusher.Flush()
}

func streamEventType(evt agent.StreamEvent) string {
	switch evt.Type {
	case agent.EventToken:
		return "text"
	case agent.EventToolStart:
		return "tool_call"
	case agent.EventToolOutput:
		return "tool_call"
	case agent.EventDone:
		return "done"
	case agent.EventError:
		return "error"
	case agent.EventStatus:
		return "status"
	case agent.EventReasoning:
		return "reasoning"
	default:
		return "unknown"
	}
}

func buildSSEData(evt agent.StreamEvent) map[string]interface{} {
	data := map[string]interface{}{}
	switch evt.Type {
	case agent.EventStatus:
		if evt.Phase != "" {
			data["phase"] = evt.Phase
		}
	case agent.EventReasoning:
		if evt.Content != "" {
			data["content"] = evt.Content
		}
	case agent.EventToken:
		if evt.Content != "" {
			data["content"] = evt.Content
		}
	case agent.EventToolStart, agent.EventToolOutput:
		if evt.ToolName != "" {
			data["name"] = evt.ToolName
		}
		if evt.ToolInput != nil {
			data["input"] = evt.ToolInput
		}
		if evt.ToolOutput != "" {
			data["output"] = evt.ToolOutput
		}
		// Fallback: use ToolCall struct for backward compat fields
		if evt.ToolCall != nil {
			if evt.ToolName == "" {
				data["name"] = evt.ToolCall.Name
			}
			if evt.ToolInput == nil {
				data["input"] = evt.ToolCall.Arguments
			}
		}
		// Legacy metadata output
		if evt.Metadata != nil {
			if output, ok := evt.Metadata["output"]; ok && evt.ToolOutput == "" {
				data["output"] = output
			}
		}
	case agent.EventDone:
		if evt.Result != nil {
			data["model"] = evt.Result.Model
			data["tokens"] = evt.Result.TotalTokens
			data["tool_calls"] = evt.Result.ToolCalls
			data["steps"] = evt.Result.Steps
			if evt.Result.Response != "" {
				data["response"] = evt.Result.Response
			}
			if evt.Result.Blocked {
				data["blocked"] = true
				if evt.Result.BlockReason != "" {
					data["block_reason"] = evt.Result.BlockReason
				}
			}
		}
		// If Model/Tokens set directly on event, override
		if evt.Model != "" {
			data["model"] = evt.Model
		}
		if evt.Tokens > 0 {
			data["tokens"] = evt.Tokens
		}
	case agent.EventError:
		if evt.Error != nil {
			data["message"] = evt.Error.Error()
		}
	}
	if len(evt.Metadata) > 0 {
		data["metadata"] = evt.Metadata
	}
	return data
}

// ---------------------------------------------------------------------------
// Agent management
// ---------------------------------------------------------------------------

// getOrCreateAgent returns the agent for a session, creating one if needed.
func (s *Server) getOrCreateAgent(ctx context.Context, sessionID string) (*agent.Agent, *RPCError) {
	s.mu.RLock()
	a, ok := s.agents[sessionID]
	s.mu.RUnlock()
	if ok {
		return a, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock.
	if a, ok := s.agents[sessionID]; ok {
		return a, nil
	}

	a, err := s.createAgent(ctx, sessionID)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}
	s.agents[sessionID] = a
	return a, nil
}

// saveSessionState persists the current agent state into the session store.
// Best-effort — failures are logged but not returned.
func (s *Server) saveSessionState(ctx context.Context, sessionID string, a *agent.Agent) {
	sess, err := s.sessionStore.Load(ctx, sessionID)
	if err != nil {
		return
	}
	sess.Model = a.Model()
	sess.TurnCount = len(a.History())
	_ = s.sessionStore.Save(ctx, sess)
}

// ---------------------------------------------------------------------------
// Middleware helper
// ---------------------------------------------------------------------------

func (s *Server) withMiddleware(fn http.HandlerFunc) http.HandlerFunc {
	return withCORS(withPanicRecovery(withRequestLog(fn)))
}

// ---------------------------------------------------------------------------
// JSON response writer
// ---------------------------------------------------------------------------

func writeRPCResponse(w http.ResponseWriter, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
