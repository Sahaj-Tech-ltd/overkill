package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/credentials"
	"github.com/Sahaj-Tech-ltd/overkill/internal/credit"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/features"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hotreload"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/mcp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/shadows"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/speculative"
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools/tts"
	"github.com/Sahaj-Tech-ltd/overkill/internal/web"
)

// Server is the JSON-RPC 2.0 HTTP server that wraps the existing internal
// packages behind a clean API for the Ink TUI to consume.
type Server struct {
	flowQueue             *agent.FlowQueue
	flowCancel            context.CancelFunc
	flowDone              chan struct{}
	dashboardGoalReader   func(context.Context, string) (*agent.Goal, error)
	dashboardPlanReader   func(context.Context, string) (*plan.Plan, error)
	callbackDestinations  map[string]string
	callbackTransport     http.RoundTripper
	coordinationStore     *coordination.Store
	canvasStore           *web.CanvasStore
	backgroundMu          sync.Mutex
	backgroundStarted     bool
	backgroundClosed      bool
	backgroundCancel      context.CancelFunc
	backgroundDone        chan struct{}
	backgroundError       bool
	presenceMu            sync.Mutex
	presenceObserver      string
	presenceContext       context.Context
	closeOnce             sync.Once
	admissionMu           sync.Mutex
	sessionOperations     map[string]bool
	sessionCancels        map[string]*sessionCancellation
	mu                    sync.RWMutex
	configCredentialStore credentials.Store
	configCreateMu        sync.Mutex // serializes config.create calls to prevent concurrent overwrites (H11)
	cfg                   *config.Config
	sessionStore          session.Store
	agents                map[string]*agent.Agent // session ID → agent
	toolRegistry          *tools.Registry
	sessionRuntimeFactory SessionRuntimeFactory
	sessionRuntimes       map[string]*SessionRuntime
	mcpCancel             context.CancelFunc
	mcpDone               chan struct{}
	mcpManager            *mcp.Manager // MCP server manager (nil when no servers configured)
	db                    *sql.DB      // shared database pool (opened once at startup)
	httpServer            *http.Server
	port                  int // protected by mu
	address               string
	bridgeAddress         string
	bridgeCancel          context.CancelFunc
	bridgeDone            <-chan error
	authToken             string
	initErr               error
	ready                 chan struct{}
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
	pendingQuestions   map[string]chan agent.Answer
	pendingQuestionIDs map[string]string // protected by mu; one-shot generation fence
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
	// syncErrors tracks the most recent auto-push failure per session,
	// exposed via agent.status so the agent can surface it to the user.
	syncErrors   map[string]string // session ID → last error
	syncErrorsMu sync.RWMutex

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

	// listenAddr is the host:port passed to net.Listen. Defaults to
	// "localhost:0" when empty (OS-chosen port), which is what the TUI uses.
	listenAddr string

	// tasksStore powers the todo.* RPC endpoints (§8.3). Optional.
	tasksStore TaskStore

	// skillsStore powers the skills.* RPC endpoints. Optional.
	skillsStore *skillsStore

	// askBridge is the TUI-side bridge for the ask_user tool. When set,
	// the ask_user tool surfaces prompts in the TUI instead of returning
	// Cancel:true. Set via SetAskBridge from cmd/overkill/tui.go.
	askBridge func(ctx context.Context, prompt string, choices []string) (text string, index int, cancel bool)
}

// SetAskBridge installs a TUI-side bridge for the ask_user tool.
// When nil, the ask_user tool always returns Cancel:true.
func (s *Server) SetAskBridge(bridge func(ctx context.Context, prompt string, choices []string) (text string, index int, cancel bool)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.askBridge = bridge
}

// AskUser bridges a tool-level ask_user call into the TUI prompt system.
// Blocks until the user answers or the context is cancelled.
func (s *Server) AskUser(ctx context.Context, prompt string, choices []string) (text string, index int, cancel bool) {
	s.mu.RLock()
	bridge := s.askBridge
	s.mu.RUnlock()
	if bridge == nil {
		return "", -1, true
	}
	return bridge(ctx, prompt, choices)
}

// Addr returns the address the server is listening on.
// Only valid after Start() has been called.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.address != "" {
		return s.address
	}
	return fmt.Sprintf("http://localhost:%d", s.port)
}

// Ready closes after the listener and HTTP server are initialized. Callers
// must also watch Start's result for startup failures.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// AuthToken is passed only through the launched frontend's environment.
func (s *Server) AuthToken() string { return s.authToken }

// ServerConfig holds everything the API server needs at startup.
type ServerConfig struct {
	FlowQueue *agent.FlowQueue
	// ConfigCredentialStore explicitly injects the onboarding vault; nil uses OS credentials.
	ConfigCredentialStore credentials.Store
	Canvas                *web.CanvasStore
	DashboardGoalReader   func(context.Context, string) (*agent.Goal, error)
	DashboardPlanReader   func(context.Context, string) (*plan.Plan, error)
	// CoordStore enables durable delivery for explicitly bound peers.
	CallbackDestinations  map[string]string
	CallbackTransport     http.RoundTripper // Explicit application/test injection; nil uses protected HTTPS.
	CoordStore            *coordination.Store
	Config                *config.Config
	SessionStore          session.Store
	Tools                 *tools.Registry
	SessionRuntimeFactory SessionRuntimeFactory
	LearningStore         *learning.Store // optional: correction learning store (§6.5)
	// ListenAddr is the host:port the server binds to (e.g. "127.0.0.1:7777").
	// When empty, the OS picks a random port on localhost (equivalent to
	// "localhost:0"). The TUI path leaves this unset to preserve existing behaviour.
	ListenAddr string
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

	// TasksStore powers the todo.* RPC endpoints (§8.3 todo panel).
	// Optional — nil-safe, endpoints return empty lists when unset.
	TasksStore TaskStore

	// SkillsRegistry powers the skills.* RPC endpoints. Optional — nil-safe.
	SkillsRegistry *skills.Registry

	// DB is the shared Postgres connection pool. Optional — when set, enables
	// Postgres-backed agent stores (GoalStore, etc.).
	DB *sql.DB
}

// NewServer creates a new API server. Call Start to begin listening.
func NewServer(sc ServerConfig) *Server {
	token := os.Getenv("OVERKILL_API_TOKEN")
	var tokenErr error
	if token == "" {
		b := make([]byte, 32)
		if _, tokenErr = rand.Read(b); tokenErr == nil {
			token = hex.EncodeToString(b)
		}
	}
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

	// ── MCP: Model Context Protocol server manager ──
	var mcpMgr *mcp.Manager
	if sc.Config != nil && len(sc.Config.MCP.Servers) > 0 {
		mcpMgr = mcp.NewManager(sc.Config.MCP)

	}

	if sc.SkillsRegistry == nil {
		sc.SkillsRegistry = skills.NewRegistry()
	}
	extensionManager := sc.ExtensionsManager.Clone()
	if sc.ExtensionsManager == nil {
		home, _ := config.ConfigDir()
		extensionManager = extensions.NewConfiguredManager(sc.SkillsRegistry, sc.Config, home)
	}
	if sc.Config != nil {
		extensionManager.AddBackend(extensions.NewMCPBackend(sc.Config.MCP, mcpMgr))
	}
	return &Server{
		flowQueue:             sc.FlowQueue,
		dashboardGoalReader:   sc.DashboardGoalReader,
		dashboardPlanReader:   sc.DashboardPlanReader,
		callbackDestinations:  sc.CallbackDestinations,
		callbackTransport:     sc.CallbackTransport,
		coordinationStore:     sc.CoordStore,
		canvasStore:           sc.Canvas,
		cfg:                   sc.Config,
		configCredentialStore: sc.ConfigCredentialStore,
		authToken:             token,
		initErr:               tokenErr,
		ready:                 make(chan struct{}),
		sessionStore:          sc.SessionStore,
		agents:                make(map[string]*agent.Agent),
		toolRegistry:          reg,
		sessionRuntimeFactory: sc.SessionRuntimeFactory,
		sessionRuntimes:       make(map[string]*SessionRuntime),
		learningStore:         sc.LearningStore,
		featureMgr:            sc.FeatureManager,
		extensionsMgr:         extensionManager,
		readCache:             sc.ReadCache,
		pendingQuestions:      make(map[string]chan agent.Answer),
		pendingQuestionData:   make(map[string]*agent.Question),
		memoEngine:            sc.MemoEngine,
		creditAnalyzer:        credit.NewAnalyzer(),
		syncMgr:               sc.SyncManager,
		syncErrors:            make(map[string]string),
		hotReloadBus:          sc.HotReloadBus,
		subagentManager:       sc.SubagentManager,
		costTracker:           sc.CostTracker,
		listenAddr:            sc.ListenAddr,
		tasksStore:            sc.TasksStore,
		skillsStore:           newSkillsStore(sc.SkillsRegistry),
		mcpManager:            mcpMgr,
		db:                    sc.DB,
	}
}

// Start binds to localhost:0 (OS-chosen port), prints the address to stderr,
// and blocks until the context is cancelled or SIGINT/SIGTERM is received.
// Graceful shutdown is handled automatically.
func (s *Server) Start(ctx context.Context) error {
	defer s.Close()
	if s.initErr != nil {
		return fmt.Errorf("api: generate authentication credential: %w", s.initErr)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", s.withMiddleware(s.handleRPC))
	mux.HandleFunc("/sse", s.withMiddleware(s.handleSSE))
	mux.HandleFunc("/stream", s.withMiddleware(s.handleStream))
	mux.HandleFunc("/health", s.withMiddleware(s.handleHealth))
	mux.HandleFunc("/api/goal", s.withMiddleware(s.handleAPIGoal))
	mux.HandleFunc("/api/plan", s.withMiddleware(s.handleAPIPlan))

	// Shadows — hidden easter eggs (DOOM via js-dos).
	shadows.ServeDoomAssets(mux)

	listenAddr := s.listenAddr
	if listenAddr == "" {
		listenAddr = "localhost:0"
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("api: listen: %w", err)
	}

	addr := ln.Addr().(*net.TCPAddr)
	s.mu.Lock()
	s.port = addr.Port
	s.address = "http://" + ln.Addr().String()
	s.mu.Unlock()
	log.Printf("API listening on %s", s.Addr())

	if err := s.StartBackground(ctx); err != nil {
		_ = ln.Close()
		return err
	}

	runCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Bridge gateway: auto-start HTTP webhook if configured ──
	if s.cfg != nil && s.cfg.Gateways.Bridge.Enabled {
		if err := s.startBridge(runCtx); err != nil {
			_ = ln.Close()
			return err
		}
	}

	s.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // no timeout — SSE streams may be long-lived
	}

	// Own the shutdown goroutine and wait for its drain before returning, so
	// callers can safely release shared stores after Start returns.
	serveDone := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-serveDone:
			return
		case <-runCtx.Done():
		}
		if s.mcpManager != nil {
			s.mcpManager.Stop()
		}
		sdCtx, sdCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer sdCancel()
		if err := s.httpServer.Shutdown(sdCtx); err != nil {
			_ = s.httpServer.Close()
		}
	}()
	close(s.ready)
	err = s.httpServer.Serve(ln)
	close(serveDone)
	<-shutdownDone
	bridgeErr := s.stopBridge()
	if bridgeErr != nil && !errors.Is(bridgeErr, context.Canceled) {
		return bridgeErr
	}
	if err == http.ErrServerClosed {
		return nil
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
	case "coordination.transport.bind", "coordination.transport.clear":
		result, rpcErr := s.handleCoordinationTransport(ctx, req.Method, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "coordination.reconciliation", "coordination.reconciliation.candidates", "coordination.reconcile":
		resp.Result, resp.Error = s.handleUsageReconciliation(ctx, req.Method, req.Params)
	case "canvas.state", "canvas.apply", "canvas.reset":
		resp.Result, resp.Error = s.handleCanvas(ctx, req.Method, req.Params)
	case "coordination.callback.rotate", "coordination.callback.subscribe", "coordination.callback.list", "coordination.callback.deliveries", "coordination.callback.cancel", "coordination.callback.retry":
		resp.Result, resp.Error = s.handleCoordinationCallback(ctx, req.Method, req.Params)
	case "coordination.review", "coordination.graph", "coordination.register", "coordination.peers", "coordination.messages", "coordination.send", "coordination.cancel", "coordination.events", "coordination.contract", "coordination.escalations", "coordination.usage", "coordination.message":
		resp.Result, resp.Error = s.handleCoordination(ctx, req.Method, req.Params)
	case "agent.send":
		result, rpcErr := s.handleAgentSend(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "agent.abort":
		result, rpcErr := s.handleAgentAbort(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "flow.list":
		resp.Result, resp.Error = s.handleFlowList(ctx, req.Params)
	case "agent.undo":
		result, rpcErr := s.handleAgentUndo(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "workers.list", "workers.inbox", "workers.send", "workers.acknowledge":
		result, rpcErr := s.handleWorkers(ctx, req.Method, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "terminal.list", "terminal.read", "terminal.input", "terminal.resize", "terminal.control", "terminal.interrupt", "terminal.close", "terminal.handoff", "terminal.forget":
		result, rpcErr := s.handleTerminal(ctx, req.Method, req.Params)
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
	case "models.select":
		result, rpcErr := s.handleModelsSelect(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "session.audit":
		resp.Result, resp.Error = s.handleSessionAudit(ctx, req.Params)
	case "session.messages":
		result, rpcErr := s.handleSessionMessages(ctx, req.Params)
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
	case "todo.add":
		result, rpcErr := s.handleTodoAdd(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "todo.update":
		result, rpcErr := s.handleTodoUpdate(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "todo.toggle":
		result, rpcErr := s.handleTodoToggle(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "todo.delete":
		result, rpcErr := s.handleTodoDelete(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "todo.list":
		result, rpcErr := s.handleTodoList(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "skills.list":
		result, rpcErr := s.handleSkillsList(ctx, req.Params)
		resp.Result, resp.Error = result, rpcErr
	case "evolution.list", "evolution.generate", "evolution.review", "evolution.apply", "evolution.rollback":
		resp.Result, resp.Error = s.handleEvolution(ctx, req.Method, req.Params)
	case "extensions.list", "extensions.toggle":
		resp.Result, resp.Error = s.handleExtensions(ctx, req.Method, req.Params)
	case "skills.toggle":
		result, rpcErr := s.handleSkillsToggle(ctx, req.Params)
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

	release, err := s.beginSessionOperation(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer release()
	ctx, cancel := s.bindSessionCancellation(r.Context(), sessionID)
	defer cancel()

	// Allow the client to abort via POST /rpc with agent.abort, but also
	// wire the request context so disconnecting the HTTP connection cancels
	// the agent.
	a, rpcErr := s.getOrCreateAdmittedAgent(ctx, sessionID)
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

	closed := s.consumeStreamEvents(ctx, w, flusher, events, func() error {
		return s.saveSessionState(context.WithoutCancel(ctx), sessionID, a)
	})

	// Drain the events channel with a short timeout so the agent
	// goroutine that may still be writing to it doesn't leak.
	// Without this, a slow SSE consumer disconnect could leave the
	// agent goroutine blocked on send forever.
	s.drainChannel(events)

	// Persist after stream completes.
	if !closed {
		if err := s.saveSessionState(context.WithoutCancel(ctx), sessionID, a); err != nil {
			log.Printf("stream persistence failed: %v", err)
		}
	}

	// P3: credit assignment after SSE stream completes.
	s.mu.RLock()
	prov := s.cfg.Agent.DefaultProvider
	mod := s.cfg.Agent.DefaultModel
	s.mu.RUnlock()
	s.recordCreditTurn(a, prov, mod)

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

	release, err := s.beginSessionOperation(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer release()
	ctx, cancel := s.bindSessionCancellation(r.Context(), sessionID)
	defer cancel()

	a, rpcErr := s.getOrCreateAdmittedAgent(ctx, sessionID)
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

	closed := s.consumeStreamEvents(ctx, w, flusher, events, func() error {
		return s.saveSessionState(context.WithoutCancel(ctx), sessionID, a)
	})

	// Drain the events channel with a short timeout so the agent
	// goroutine that may still be writing to it doesn't leak.
	s.drainChannel(events)

	// Persist after stream completes.
	if !closed {
		if err := s.saveSessionState(context.WithoutCancel(ctx), sessionID, a); err != nil {
			log.Printf("stream persistence failed: %v", err)
		}
	}

	// P3: credit assignment after SSE stream completes.
	s.mu.RLock()
	prov := s.cfg.Agent.DefaultProvider
	mod := s.cfg.Agent.DefaultModel
	s.mu.RUnlock()
	s.recordCreditTurn(a, prov, mod)

	// Mirror CLI path: optional non-blocking sync push.
	s.maybeAutoPush(sessionID)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(HealthResult{
		Coordination: s.CoordinationStatus(),
		Status:       "ok",
		Version:      s.cfg.Version,
	})
}

// ---------------------------------------------------------------------------
// SSE helpers
// ---------------------------------------------------------------------------

// consumeStreamEvents reads from the agent's event channel and writes SSE
// formatted events. It blocks until the channel closes or ctx is done.
func (s *Server) consumeStreamEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, events <-chan agent.StreamEvent, persist func() error) bool {
	var terminal *agent.StreamEvent
	failed := false
	for {
		select {
		case <-ctx.Done():
			return false
		case evt, ok := <-events:
			if !ok {
				if persist != nil {
					if err := persist(); err != nil {
						s.writeSSEEvent(w, flusher, agent.StreamEvent{Type: agent.EventError, Error: err})
						return true
					}
				}
				if !failed {
					if terminal != nil {
						s.writeSSEEvent(w, flusher, *terminal)
					} else {
						s.writeSSEEvent(w, flusher, agent.StreamEvent{Type: agent.EventError, Error: fmt.Errorf("agent stream ended without a completion event")})
					}
				}
				return true
			}
			if evt.Type == agent.EventDone {
				copy := evt
				terminal = &copy
				continue
			}
			if evt.Type == agent.EventError {
				failed = true
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
		if evt.Content != "" {
			data["content"] = evt.Content
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
		if evt.Type == agent.EventToolStart {
			data["phase"] = "start"
		} else {
			data["phase"] = "output"
			data["output"] = evt.ToolOutput
		}
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

// getOrCreateAgent safely resolves a runtime for non-admitted readers. It may
// rebuild a stale catalogue, so it must never discard an active turn's agent.
func (s *Server) getOrCreateAgent(ctx context.Context, sessionID string) (*agent.Agent, *RPCError) {
	release, err := s.beginSessionOperation(sessionID)
	if err != nil {
		return nil, &RPCError{Code: InvalidRequest, Message: "Session is busy; retry runtime inspection after its active turn finishes."}
	}
	defer release()
	return s.getOrCreateAdmittedAgent(ctx, sessionID)
}

// getOrCreateAdmittedAgent requires the caller to hold session admission through
// execution and persistence. Keeping this distinct prevents recursive admission.
func (s *Server) getOrCreateAdmittedAgent(ctx context.Context, sessionID string) (*agent.Agent, *RPCError) {
	s.mu.RLock()
	a, ok := s.agents[sessionID]
	runtime := s.sessionRuntimes[sessionID]
	s.mu.RUnlock()
	if ok && runtime != nil && runtime.CheckCurrent != nil {
		current, err := runtime.CheckCurrent(ctx)
		if err != nil {
			return nil, &RPCError{Code: InternalError, Message: "Session coordination transport is unavailable."}
		}
		if !current {
			s.discardSessionRuntime(sessionID)
			ok = false
		}
	}
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
// PCA-1: Also auto-titles the session on first turn using GenerateTitle.
func (s *Server) saveSessionState(ctx context.Context, sessionID string, a *agent.Agent) error {
	return PersistAgentSession(ctx, s.sessionStore, sessionID, a)
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
		s.syncErrorsMu.Lock()
		s.syncErrors[sessionID] = err.Error()
		s.syncErrorsMu.Unlock()
	})
}

// ---------------------------------------------------------------------------
// Middleware helper
// ---------------------------------------------------------------------------

func (s *Server) withMiddleware(fn http.HandlerFunc) http.HandlerFunc {
	return withCORS(withAPIAuth(s.authToken, withPanicRecovery(withRequestLog(fn))))
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

// startBridge uses the same persisted session router as the gateway commands.
// Configuration/router errors abort startup rather than falling back to memory.
func (s *Server) startBridge(ctx context.Context) error {
	bridgeCtx, cancel := context.WithCancel(ctx)
	b, err := s.newAPIBridge(bridgeCtx)
	if err != nil {
		cancel()
		return err
	}
	done, err := b.Start(bridgeCtx)
	if err != nil {
		cancel()
		return err
	}
	s.mu.Lock()
	s.bridgeAddress = b.Addr()
	s.bridgeCancel = cancel
	s.bridgeDone = done
	s.mu.Unlock()
	return nil
}

// stopBridge drains accepted bridge work before shared stores/runtimes close.
func (s *Server) stopBridge() error {
	s.mu.Lock()
	cancel, done := s.bridgeCancel, s.bridgeDone
	s.bridgeCancel = nil
	s.bridgeDone = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		return <-done
	}
	return nil
}
