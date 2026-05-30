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
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/credit"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/features"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hotreload"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/speculative"
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools/tts"
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
	// learningStore is wired into every agent created by this server
	// so the TUI's agent loop can record and retrieve corrections (§6.5).
	learningStore *learning.Store
	// featureMgr gates runtime feature flags (P1).
	featureMgr *features.Manager
	// extensionsMgr is the unified extensions registry (P2).
	extensionsMgr *extensions.Manager
	// readCache is the speculative read cache (P2).
	readCache *speculative.ReadCache
	// pendingQuestions maps session ID → channel that receives the user's
	// answer. Set by the agent's QuestionFunc, resolved by handleAgentAnswer.
	pendingQuestions map[string]chan agent.Answer
	// pendingQuestionData stores the question itself for polling.
	pendingQuestionMu   sync.RWMutex
	pendingQuestionData map[string]*agent.Question // session → question

	// Memo the Elephant — thinking indicator phrase engine.
	memoEngine *personality.MemoEngine

	// creditAnalyzer folds per-turn session records for retrospective
	// lift/frequency analytics (§8.6 Wave 4). In-memory — the TUI server
	// doesn't persist credit state across restarts.
	creditAnalyzer *credit.Analyzer

	// syncMgr, when set, enables auto-push of session state after each
	// successful turn via sync.AutoPushIfEnabled. Nil-safe — when unset
	// or Sync.AutoPush is false, auto-push is a no-op.
	syncMgr *syncpkg.Manager

	// hotReloadBus, when set, is wired into every agent created by the
	// server so user.yaml changes are live-applied at turn boundaries.
	// Bonus: mirrors the CLI path's hotreload wiring.
	hotReloadBus *hotreload.Bus

	// subagentManager tracks active sub-agents for the TUI subagent panel.
	subagentManager agent.SubagentManager

	// currentMode is the session-level plan/build mode. "plan" means
	// the agent plans/analyzes only without executing writes; "build"
	// means full execution is enabled. Default "" means build.
	currentMode string // "plan" or "" (build)

	// costTracker, when set, powers the session.usage RPC endpoint.
	// Nil disables the endpoint (returns a helpful error).
	costTracker cost.Tracker
}

// Addr returns the address the server is listening on.
// Only valid after Start() has been called.
func (s *Server) Addr() string {
	return fmt.Sprintf("http://localhost:%d", s.port)
}

// ServerConfig holds everything the API server needs at startup.
type ServerConfig struct {
	Config        *config.Config
	SessionStore  session.Store
	Tools         *tools.Registry
	LearningStore *learning.Store // optional: correction learning store (§6.5)
	// FeatureManager gates runtime feature flags (P1). Optional.
	FeatureManager *features.Manager
	// ExtensionsManager is the unified extensions registry (P2). Optional.
	ExtensionsManager *extensions.Manager
	// ReadCache is the speculative read cache (P2). Optional.
	ReadCache *speculative.ReadCache
	// MemoEngine powers the Memo elephant thinking indicator. Optional.
	// If nil, memo RPC endpoints return a static fallback.
	MemoEngine *personality.MemoEngine
	// SyncManager enables auto-push of session state after each turn.
	// Optional — when nil or cfg.Sync.AutoPush is false, auto-push is a no-op.
	SyncManager *syncpkg.Manager
	// HotReloadBus, when set, is wired into every agent created by the
	// server so user.yaml changes are live-applied. Optional.
	HotReloadBus *hotreload.Bus
	// SubagentManager, when set, enables sub-agent tracking in the API
	// and TUI subagent panel. Optional — nil-safe.
	SubagentManager agent.SubagentManager

	// CostTracker, when set, powers the session.usage RPC endpoint.
	// Optional — nil disables the endpoint.
	CostTracker cost.Tracker
}

// NewServer creates a new API server. Call Start to begin listening.
func NewServer(sc ServerConfig) *Server {
	reg := sc.Tools
	if reg == nil {
		reg = tools.NewRegistry()
	}

	// Register TTS tool if configured.
	if sc.Config != nil && sc.Config.TTS.Provider != "" {
		ttsTool := tts.New(sc.Config.TTS)
		if err := reg.Register(ttsTool); err != nil {
			log.Printf("api: failed to register tts tool: %v", err)
		}
	}

	return &Server{
		cfg:           sc.Config,
		sessionStore:  sc.SessionStore,
		agents:        make(map[string]*agent.Agent),
		toolRegistry:  reg,
		learningStore: sc.LearningStore,
		featureMgr:    sc.FeatureManager,
		extensionsMgr: sc.ExtensionsManager,
		readCache:          sc.ReadCache,
		pendingQuestions:   make(map[string]chan agent.Answer),
		pendingQuestionData: make(map[string]*agent.Question),
		memoEngine:         sc.MemoEngine,
		creditAnalyzer:     credit.NewAnalyzer(),
		syncMgr:            sc.SyncManager,
		hotReloadBus:       sc.HotReloadBus,
		subagentManager:    sc.SubagentManager,
		costTracker:        sc.CostTracker,
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
	mux.HandleFunc("/api/goal", s.withMiddleware(s.handleAPIGoal))
	mux.HandleFunc("/api/plan", s.withMiddleware(s.handleAPIPlan))

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
	bodyReader := http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB
	if err := json.NewDecoder(bodyReader).Decode(&req); err != nil {
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
	case "agent.undo":
		result, rpcErr := s.handleAgentUndo(ctx, req.Params)
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
	case "agent.fork":
		result, rpcErr := s.handleAgentFork(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "session.fork":
		result, rpcErr := s.handleAgentFork(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "models.select":
		result, rpcErr := s.handleModelsSelect(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "session.load":
		result, rpcErr := s.handleSessionLoad(ctx, req.Params)
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
	case "config.exists":
		result, rpcErr := s.handleConfigExists(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "config.create":
		result, rpcErr := s.handleConfigCreate(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "config.theme":
		result, rpcErr := s.handleConfigTheme(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "agent.subagents":
		result, rpcErr := s.handleAgentSubagents(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "gateway.test":
		result, rpcErr := s.handleGatewayTest(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "wizard.catalog":
		result, rpcErr := s.handleWizardCatalog(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "wizard.quick-setup":
		result, rpcErr := s.handleWizardQuickSetup(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "self.eval.status":
		result, rpcErr := s.handleSelfEvalStatus(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "tests.results":
		result, rpcErr := s.handleTestResults(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "sequential.queue":
		result, rpcErr := s.handleSequentialQueue(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "clarify.poll":
		result, rpcErr := s.handleClarifyPoll(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "agent.answer":
		result, rpcErr := s.handleAgentAnswer(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "agent.steer":
		result, rpcErr := s.handleAgentSteer(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "memo.phrase":
		result, rpcErr := s.handleMemoPhrase(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "memo.learn":
		result, rpcErr := s.handleMemoLearn(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "thinking.set_level":
		result, rpcErr := s.handleThinkingSetLevel(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "mode.set":
		result, rpcErr := s.handleModeSet(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "session.usage":
		result, rpcErr := s.handleSessionUsage(ctx, req.Params)
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

	// Drain the events channel with a short timeout so the agent
	// goroutine that may still be writing to it doesn't leak.
	// Without this, a slow SSE consumer disconnect could leave the
	// agent goroutine blocked on send forever.
	s.drainChannel(events)

	// Persist after stream completes.
	s.saveSessionState(ctx, sessionID, a)

	// P3: credit assignment after SSE stream completes.
	s.recordCreditTurn(a, s.cfg.Agent.DefaultProvider, s.cfg.Agent.DefaultModel)

	// Mirror CLI path: optional non-blocking sync push.
	s.maybeAutoPush(sessionID)
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

	// Drain the events channel with a short timeout so the agent
	// goroutine that may still be writing to it doesn't leak.
	s.drainChannel(events)

	// Persist after stream completes.
	s.saveSessionState(ctx, sessionID, a)

	// P3: credit assignment after SSE stream completes.
	s.recordCreditTurn(a, s.cfg.Agent.DefaultProvider, s.cfg.Agent.DefaultModel)

	// Mirror CLI path: optional non-blocking sync push.
	s.maybeAutoPush(sessionID)
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

// drainChannel reads and discards any remaining events from the channel
// with a short timeout. This unblocks the agent goroutine that may still
// be trying to send on the channel after the SSE consumer has disconnected.
func (s *Server) drainChannel(events <-chan agent.StreamEvent) {
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-timeout:
			return
		case _, ok := <-events:
			if !ok {
				return
			}
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
// PCA-1: Also auto-titles the session on first turn using GenerateTitle.
func (s *Server) saveSessionState(ctx context.Context, sessionID string, a *agent.Agent) {
	sess, err := s.sessionStore.Load(ctx, sessionID)
	if err != nil {
		return
	}
	sess.Model = a.Model()
	sess.TurnCount = len(a.History())

	// Auto-title on first substantive turn (title is empty, history has content).
	if sess.Title == "" && sess.TurnCount > 0 {
		if title, titleErr := a.GenerateTitle(ctx); titleErr == nil && title != "" {
			sess.Title = title
		}
	}

	_ = s.sessionStore.Save(ctx, sess)
}

// recordCreditTurn folds per-turn credit actions into the in-memory analyzer.
// Mirrors the CLI path's finalizeSession credit folding (cmd/overkill/run.go)
// but fires after every turn instead of only at session exit. Best-effort:
// panics are recovered, errors are silent.
func (s *Server) recordCreditTurn(a *agent.Agent, providerName, modelName string) {
	if s.creditAnalyzer == nil {
		return
	}
	toolCalls, errs, recovs, turns, _ := a.SessionMetrics()
	actions := make([]credit.Action, 0)
	if toolCalls > 0 {
		actions = append(actions, credit.Action{Tag: "tool_call", Category: "tool"})
	}
	if errs > 0 {
		actions = append(actions, credit.Action{Tag: "error", Category: "error"})
	}
	if recovs > 0 {
		actions = append(actions, credit.Action{Tag: "recovery", Category: "recovery"})
	}
	outcome := credit.OutcomeUnknown
	if turns >= 3 {
		if errs > 0 && recovs == 0 {
			outcome = credit.OutcomeFailure
		} else if errs == 0 || recovs > 0 {
			outcome = credit.OutcomeSuccess
		}
	}
	_ = turns // used for outcome gating
	s.creditAnalyzer.Fold(credit.SessionRecord{
		SessionID: a.SessionID(),
		Outcome:   outcome,
		Actions:   actions,
		Tags:      []string{providerName, modelName},
	})
}

// maybeAutoPush fires a non-blocking sync push for the given session when
// both the sync manager and config opt in. Mirrors run.go's per-turn
// sync.AutoPushIfEnabled call.
func (s *Server) maybeAutoPush(sessionID string) {
	syncpkg.AutoPushIfEnabled(s.cfg, s.syncMgr, sessionID, func(err error) {
		log.Printf("sync auto-push failed for session %s: %v", sessionID, err)
	})
}

// ---------------------------------------------------------------------------
// Middleware helper
// ---------------------------------------------------------------------------

func (s *Server) withMiddleware(fn http.HandlerFunc) http.HandlerFunc {
	return withCORS(withAPIAuth(withPanicRecovery(withRequestLog(fn))))
}

// ---------------------------------------------------------------------------
// JSON response writer
// ---------------------------------------------------------------------------

func writeRPCResponse(w http.ResponseWriter, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ---------------------------------------------------------------------------
// Memo the Elephant — thinking indicator RPC handlers
// ---------------------------------------------------------------------------

// memoPhraseParams is the input for memo.phrase.
type memoPhraseParams struct {
	Input  string `json:"input"`
	Action string `json:"action"` // optional: tool call action name
}

// memoLearnParams is the input for memo.learn (self-improvement).
type memoLearnParams struct {
	Patterns []string `json:"patterns"`
	Phrases  []string `json:"phrases"`
	Category string   `json:"category"`
}

func (s *Server) handleMemoPhrase(_ context.Context, params json.RawMessage) (interface{}, *RPCError) {
	var p memoPhraseParams
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}

	// Fallback when no engine is wired.
	if s.memoEngine == nil {
		defaults := personality.DefaultMemoDefaults()
		return map[string]interface{}{
			"phrase":   defaults[0],
			"category": "default",
		}, nil
	}

	if p.Action != "" {
		r := s.memoEngine.ActionMatch(p.Action)
		return map[string]interface{}{
			"phrase":   r.Phrase,
			"category": r.Category,
		}, nil
	}

	if p.Input != "" {
		r := s.memoEngine.Match(p.Input)
		return map[string]interface{}{
			"phrase":   r.Phrase,
			"category": r.Category,
		}, nil
	}

	// No input — return all available phrases for the TUI side.
	return s.memoEngine.AllPhrases(), nil
}

func (s *Server) handleMemoLearn(ctx context.Context, params json.RawMessage) (interface{}, *RPCError) {
	if s.memoEngine == nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "memo engine not configured"}
	}

	var p memoLearnParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{Code: InvalidParams, Message: err.Error()}
	}

	if err := s.memoEngine.Learn(ctx, p.Patterns, p.Phrases, p.Category); err != nil {
		return nil, &RPCError{Code: InternalError, Message: err.Error()}
	}

	return map[string]interface{}{
		"learned": true,
		"count":   len(s.memoEngine.Rules()),
	}, nil
}
