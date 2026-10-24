package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/browser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/browser/devbrowser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
	"github.com/Sahaj-Tech-ltd/overkill/internal/childruntime"
	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/lsp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/memory"
	"github.com/Sahaj-Tech-ltd/overkill/internal/multimodal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/pipeline"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
	"github.com/Sahaj-Tech-ltd/overkill/internal/playbooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/speculative"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tasks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools/imagegen"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools/messaging"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools/tts"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
	"github.com/Sahaj-Tech-ltd/overkill/internal/web"
)

// runtimeServices is the common CLI/TUI/serve assembly. Shared stores are
// application-owned; BuildSession supplies every identity/workspace-dependent
// dependency afresh. It never changes process cwd or a shared current ID.
type runtimeServices struct {
	Compaction   *compaction.HistoryFactory
	cfg          *config.Config
	database     *sql.DB
	home         string
	base         tools.FactoryDeps
	Goals        *agent.GoalStore
	Tasks        *tasks.Store
	Learning     *learning.Store
	Cost         cost.Tracker
	Memo         *personality.MemoEngine
	Alarm        tools.AlarmGateway
	Coordination *coordination.Store
	FlowQueue    *agent.FlowQueue
	Canvas       *web.CanvasStore
}

func newRuntimeServices(cfg *config.Config, database *sql.DB, home string) (*runtimeServices, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	if err := cfg.Security.ValidateSupported(); err != nil {
		return nil, err
	}
	if home == "" {
		return nil, fmt.Errorf("runtime: configuration directory required")
	}
	services := &runtimeServices{cfg: cfg, database: database, home: home, Tasks: tasks.NewStore(filepath.Join(home, "tasks"))}
	compactionCtx, stopCompaction := context.WithTimeout(context.Background(), 10*time.Second)
	historyFactory, historyErr := compaction.NewHistoryFactory(compactionCtx, cfg.Compaction, database)
	stopCompaction()
	if historyErr != nil {
		return nil, fmt.Errorf("runtime: %w", historyErr)
	}
	services.Compaction = historyFactory
	base := tools.FactoryDeps{
		PlaybooksStore:     playbooks.NewStore(filepath.Join(home, "playbooks")),
		LearningsQuerier:   plan.NewLearningsStore(filepath.Join(home, "learnings")),
		FailHypoQuerier:    journal.NewFailedHypothesisStore(filepath.Join(home, "failed_hypotheses")),
		MultimodalRegistry: multimodal.DefaultRegistry(nil),
		ArchitectureWall:   walls.NewArchitectureWall(walls.ArchitectureConfig{}),
		RegressionBank:     walls.NewRegressionBank(walls.NewMemRegressionStore(), nil),
		IntrospectDir:      filepath.Join(home, "introspection"), SkillExtractDir: filepath.Join(home, "skills"),
		BrowserPolicy: tools.BrowserHostPolicy{Allowed: cfg.Browser.AllowedHosts, Blocked: cfg.Browser.BlockedHosts},
	}
	var err error
	base.ACPRemotes, err = configuredACPPeers(cfg.ACP.Peers)
	if err != nil {
		return nil, err
	}
	base.TagsManager, err = tags.NewManager(filepath.Join(home, "tags"))
	if err != nil {
		return nil, fmt.Errorf("runtime: tags: %w", err)
	}
	base.StandingOrdersStore, err = automation.NewOrdersFile(filepath.Join(home, "standing-orders.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("runtime: standing orders: %w", err)
	}
	if database != nil {
		// Runtime services may be constructed directly on a fresh owned pool.
		// Initialize the canonical session schema before stores with session FKs.
		if _, err := session.NewPostgresStore(database); err != nil {
			return nil, fmt.Errorf("runtime: sessions: %w", err)
		}
		initCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		services.Coordination, err = coordination.NewStore(initCtx, database)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("runtime: coordination: %w", err)
		}
		services.Canvas = web.NewCanvasStore(database)
		canvasCtx, stopCanvas := context.WithTimeout(context.Background(), 10*time.Second)
		err = services.Canvas.Migrate(canvasCtx)
		stopCanvas()
		if err != nil {
			return nil, fmt.Errorf("runtime: canvas: %w", err)
		}
		services.FlowQueue, err = agent.NewFlowQueue(database, 5*time.Minute)
		if err != nil {
			return nil, err
		}
		services.Goals, err = agent.NewGoalStore(database)
		if err != nil {
			return nil, err
		}
		services.Learning, err = learning.NewStore(database, 1000)
		if err != nil {
			return nil, err
		}
		services.Cost, err = cost.NewPostgresTracker(database, cfg.Cost)
		if err != nil {
			return nil, err
		}
		base.AutomationLister, err = automation.NewPostgresSOPStore(database)
		if err != nil {
			return nil, err
		}
		base.CronLister, err = cron.NewPostgresJobStore(database)
		if err != nil {
			return nil, err
		}
		regressions, err := walls.NewPostgresRegressionStore(database)
		if err != nil {
			return nil, err
		}
		base.RegressionBank = walls.NewRegressionBank(regressions, nil)
	}
	services.Memo = personality.NewMemoEngine(database)
	if gw, err := newDaemonAlarmGateway(); err == nil {
		services.Alarm = gw
	}
	if cfg.Vision.Enabled || os.Getenv("ANTHROPIC_API_KEY") != "" {
		key := cfg.Vision.APIKey
		if key == "" {
			key = os.Getenv("ANTHROPIC_API_KEY")
		}
		model := cfg.Vision.Model
		if model == "" {
			model = "claude-opus-4-5"
		}
		if key != "" {
			base.VisionDescriber = vision.NewAnthropic(key, model)
		}
	}
	base.OuroborosWall = walls.NewOuroborosWall(walls.OuroborosConfig{})
	if cfg.Ouroboros.Enabled {
		options := cfg.Ouroboros
		key := options.APIKey
		if key == "" {
			key = os.Getenv(providerEnvVar(options.Provider))
		}
		if key == "" || options.Provider == "" {
			return nil, fmt.Errorf("runtime: enabled Ouroboros requires a provider and credentials")
		}
		provider, err := providers.NewProvider(providers.FactoryConfig{Name: options.Provider, Type: options.Provider, APIKey: key, BaseURL: options.BaseURL})
		if err != nil {
			return nil, fmt.Errorf("runtime: Ouroboros provider initialization failed")
		}
		base.OuroborosWall = walls.NewOuroborosWall(walls.OuroborosConfig{Enabled: true, Provider: provider, Model: options.Model, StrictMode: options.StrictMode, SystemPrompt: options.SystemPrompt})
	}
	services.base = base
	return services, nil
}

func (r *runtimeServices) BuildSession(ctx context.Context, sess *session.Session) (*api.SessionRuntime, error) {
	if err := r.cfg.Security.ValidateSupported(); err != nil {
		return nil, err
	}
	bound, err := api.CanonicalSession(sess)
	if err != nil {
		return nil, err
	}
	deps, runtime, err := r.bindSession(ctx, bound)
	if err != nil {
		return nil, err
	}
	runtime.Tools = tools.NewDefaultRegistry(deps)
	return runtime, nil
}

func (r *runtimeServices) bindSession(ctx context.Context, sess *session.Session) (tools.FactoryDeps, *api.SessionRuntime, error) {
	if err := r.cfg.Security.ValidateSupported(); err != nil {
		return tools.FactoryDeps{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return tools.FactoryDeps{}, nil, err
	}
	sess, err := api.CanonicalSession(sess)
	if err != nil {
		return tools.FactoryDeps{}, nil, err
	}
	id, workspace := sess.ID, sess.Folder
	hookRegistry, err := hooks.NewRegistryFromDir(filepath.Join(r.home, "hooks"))
	if err != nil {
		return tools.FactoryDeps{}, nil, fmt.Errorf("runtime: user hooks: %w", err)
	}
	verifierCtx, stopVerifier := context.WithCancel(context.Background())
	verifier, err := newVerifierAdapter(verifierCtx, workspace)
	if err != nil {
		stopVerifier()
		return tools.FactoryDeps{}, nil, err
	}
	boundSuccessfully := false
	defer func() {
		if !boundSuccessfully {
			stopVerifier()
		}
	}()
	deps := r.base
	deps.CWD = workspace
	deps.Canvas = nil
	canvasBound := false
	boundCanvas := r.Canvas
	if boundCanvas != nil {
		_, canvasErr := boundCanvas.Get(ctx, id)
		if canvasErr != nil && !errors.Is(canvasErr, web.ErrCanvasSessionNotFound) {
			return tools.FactoryDeps{}, nil, fmt.Errorf("runtime: canvas binding: %w", canvasErr)
		}
		canvasBound = canvasErr == nil
	}
	if canvasBound {
		// Capture canonical application identity. A model cannot retarget this store
		// or recursively enter API admission while its model turn already owns it.
		deps.Canvas = func(callCtx context.Context, action, line string) (json.RawMessage, error) {
			var state web.CanvasState
			var err error
			switch action {
			case "state":
				state, err = boundCanvas.Get(callCtx, id)
			case "apply":
				state, err = boundCanvas.Apply(callCtx, id, line)
			case "reset":
				state, err = boundCanvas.Reset(callCtx, id)
			default:
				return nil, fmt.Errorf("unsupported canvas action")
			}
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"state": state})
		}
	}

	var coordinationCurrent func(context.Context) (bool, error)
	var terminalPeerID string
	if r.Coordination != nil {
		peer, err := r.Coordination.PeerForSession(ctx, id)
		if err != nil && !errors.Is(err, coordination.ErrNotFound) {
			return tools.FactoryDeps{}, nil, err
		}
		if err == nil {
			if peer.Workspace != workspace {
				return tools.FactoryDeps{}, nil, fmt.Errorf("runtime: coordination workspace mismatch")
			}
			coordTools, current, err := r.sessionCoordinationTools(ctx, id, peer)
			if err != nil {
				return tools.FactoryDeps{}, nil, err
			}
			coordinationCurrent = current
			deps.ExtraTools = append(append([]tools.Tool(nil), deps.ExtraTools...), coordTools...)
			deps.ExtraTools = append(deps.ExtraTools, tools.NewCoordinationContractTool(r.Coordination, peer.ID))
			deps.ExecutionGuard = tools.NewCoordinationExecutionGuard(r.Coordination, peer.ID, workspace)
			terminalPeerID = peer.ID
		}
	}
	if r.Compaction != nil && r.Compaction.Store != nil {
		deps.ExtraTools = append(deps.ExtraTools, tools.NewDAGMemoryTools(r.Compaction.Store, id)...)
	}
	deps.ProjectRootResolver = func() string { return workspace }
	deps.CheckpointSessionFn = func() string { return id }
	deps.SessionIDProvider = sessIDProvider(func() string { return id })
	deps.AlarmSessionFn = func() string { return id }
	deps.LearnRecorder = skills.NewLearnTrigger(0, nil)
	deps.AutoCommitter = automation.NewAutoCommitter(workspace, nil, nil)
	deps.TasksStore = &sessionTasks{store: r.Tasks, id: id}
	deps.SegmentsStore = memory.NewSegmentStore(filepath.Join(r.home, "segments", id), workspace)
	deps.PipelineRunner = &pipelineRunnerAdapter{exec: pipeline.NewExecutor(pipeline.Config{})}
	recorder := journal.NewFlightRecorder(filepath.Join(r.home, "journal"), id)
	deps.JournalQuerier = recorder
	deps.JournalReader = recorder
	plans := plan.NewStore(filepath.Join(r.home, "plans"), id)
	if _, err := plans.Load(); err != nil {
		return tools.FactoryDeps{}, nil, err
	}
	deps.PlanQuerier = plans
	deps.CheckpointManager, err = checkpoint.NewSessionManager(filepath.Join(r.home, "checkpoints"), 5, workspace, id)
	if err != nil {
		return tools.FactoryDeps{}, nil, err
	}
	if r.Goals != nil {
		deps.GoalQuerier = tools.NewSessionGoalQuerier(goalStoreAdapter{r.Goals}, func() string { return id })
	}
	if r.Alarm != nil {
		deps.AlarmGateway = &sessionAlarms{gateway: r.Alarm, id: id}
	}
	// LSP subprocesses belong to the session, not this assembly request.
	// Cancellation still aborts assembly until readiness has completed.
	lspCtx, stopLSP := context.WithCancel(context.Background())
	deps.LSPManager = lsp.NewManager(r.cfg.LSP, workspace)
	abortLSPStartup := context.AfterFunc(ctx, stopLSP)
	lspErr := deps.LSPManager.Start(lspCtx)
	abortLSPStartup()
	if lspErr == nil {
		lspErr = ctx.Err()
	}
	if lspErr != nil {
		deps.LSPManager.Stop()
		stopLSP()
		return tools.FactoryDeps{}, nil, fmt.Errorf("runtime: LSP startup: %w", lspErr)
	}
	defer func() {
		if !boundSuccessfully {
			deps.LSPManager.Stop()
			stopLSP()
		}
	}()
	deps.LSPQuerier = newLSPManagerAdapter(deps.LSPManager)
	options := browser.Options{Headless: r.cfg.Browser.Headless, Visible: !r.cfg.Browser.Headless, ChromePath: r.cfg.Browser.ChromePath, UserAgent: r.cfg.Browser.UserAgent, Timeout: r.cfg.Browser.Timeout}
	deps.BrowserManager = browser.NewManager(options)
	deps.DevBrowserManager = devbrowser.NewManager()
	var terminalMessages *subagent.CommunicationStore
	terminalOptions := terminal.Options{ParentOwner: terminal.Owner{Session: id, Worker: "main"}}
	if r.database != nil {
		terminalOptions.RecordHandoff = func(ctx context.Context, h terminal.Handoff) error {
			if terminalMessages == nil {
				return fmt.Errorf("terminal handoff journal unavailable")
			}
			return terminalMessages.RecordTerminalHandoff(ctx, h)
		}
	}
	if terminalPeerID != "" {
		terminalOptions.Reserve = func(ctx context.Context, _ terminal.Owner, handle string) (terminal.Reservation, error) {
			return r.Coordination.ReserveWorkspaceProcess(ctx, terminalPeerID, handle)
		}
	}
	terminals, err := terminal.New(workspace, terminalOptions)
	if err != nil {
		return tools.FactoryDeps{}, nil, err
	}
	defer func() {
		if !boundSuccessfully {
			_ = terminals.Close()
		}
	}()
	// Registered peers reserve the workspace for the whole process lifetime,
	// rather than dropping ownership when the launching tool call returns.
	if r.cfg.Security.AutonomyLevel != "readonly" {
		deps.TerminalManager = terminals
		deps.TerminalOwner = terminal.Owner{Session: id, Worker: "main"}
	}
	subagents := setupSubagentSystemForWorkspace(r.cfg, workspace)
	deps.SubagentManager = subagents.Manager
	var workerInbox agent.BoundaryInbox
	if r.database != nil {
		messages, err := subagent.NewCommunicationStore(ctx, r.database, id)
		if err != nil {
			return tools.FactoryDeps{}, nil, err
		}
		deps.WorkerMessages = messages
		terminalMessages = messages
		subagents.Manager.SetCommunicationStore(messages)
		subagents.Manager.SetDefaultToolset(append(subagents.Manager.DefaultToolset(), "worker_messages"))
		workerInbox = childruntime.WorkerInbox(messages, subagent.ParentWorker)
	}

	// Session-bound children inherit the same coordination admission identity
	// and language-server services; fresh tool adapters keep worker state separate.
	if deps.ExecutionGuard != nil {
		subagents.Manager.SetGuardedWorkerFactory(childWorkerFactory(r.cfg, workspace, deps))
	} else {
		subagents.Manager.SetWorkerFactory(childWorkerFactory(r.cfg, workspace, deps))
	}
	if deps.TerminalManager != nil && terminal.Supported() {
		subagents.Manager.SetDefaultToolset(append(subagents.Manager.DefaultToolset(), "terminal"))
	}
	if deps.LSPQuerier != nil {
		subagents.Manager.SetDefaultToolset(append(subagents.Manager.DefaultToolset(), "lsp_hover", "lsp_definition", "lsp_references"))
	}
	if deps.LSPManager != nil {
		subagents.Manager.SetDefaultToolset(append(subagents.Manager.DefaultToolset(), "lsp_symbols"))
	}
	deps.ExtraTools = append(deps.ExtraTools, tts.New(r.cfg.TTS), messaging.New(r.cfg.Gateways), imagegen.New(r.cfg.ImageGen))
	completionAuditor, stopAudit := newCompletionAuditor(r.cfg, workspace)
	sessionExtensions := extensions.NewConfiguredManager(skillRegistry, r.cfg, r.home)
	sessionExtensions.AddBackend(extensions.NewHooksBackend(hookRegistry))
	var once sync.Once
	runtime := &api.SessionRuntime{WorkerMessages: deps.WorkerMessages, BoundaryInbox: workerInbox, Terminals: terminals, CheckCurrent: coordinationCurrent, ExtensionsManager: sessionExtensions, CompletionAuditor: completionAuditor, Hooks: hookRegistry, PostWriteVerifier: verifier, CheckpointSnapshotter: &checkpointSnapshotterAdapter{mgr: deps.CheckpointManager}, GoalStore: r.Goals, SubagentManager: &subagentManagerAdapter{mgr: subagents.Manager}, Cleanup: func() {
		once.Do(func() {
			_ = terminals.Close()
			stopAudit()
			stopVerifier()
			for _, child := range subagents.Manager.ActiveChildren() {
				if child.Cancel != nil {
					child.Cancel()
				}
			}
			for _, childID := range subagents.Manager.AutonomousList() {
				subagents.Manager.AutonomousCancel(childID)
			}
			deps.LSPManager.Stop()
			stopLSP()
			deps.BrowserManager.Close()
			deps.DevBrowserManager.Shutdown()
		})
	}}
	if r.Compaction != nil {
		runtime.HistoryCompactorFactory = func(provider providers.Provider) (agent.HistoryCompactor, bool, error) {
			return r.Compaction.Build(provider, id)
		}
	}
	runtime.EvolutionFactory = func(provider providers.Provider) (*learning.Engine, error) {
		return api.NewSessionEvolution(r.cfg, r.home, sess, provider, r.Cost)
	}
	if r.FlowQueue != nil {
		model, provider := sess.Model, sess.Provider
		if model == "" {
			model = r.cfg.Agent.DefaultModel
		}
		if provider == "" {
			provider = r.cfg.Agent.DefaultProvider
		}
		runtime.FlowStore = r.FlowQueue.Bind(id, workspace, model, provider)
	}
	boundSuccessfully = true
	return deps, runtime, nil
}

func (r *runtimeServices) APIConfig(store session.Store) api.ServerConfig {
	var syncManager *syncpkg.Manager
	if r.cfg.Sync.AutoPush && r.cfg.Sync.Backend != "" {
		if backend, err := syncpkg.NewBackend(r.cfg.Sync); err == nil && backend != nil {
			syncManager = syncpkg.NewManager(store, backend)
		}
	}
	var goalReader func(context.Context, string) (*agent.Goal, error)
	if r.Goals != nil {
		goalReader = r.Goals.Get
	}
	return api.ServerConfig{FlowQueue: r.FlowQueue, DashboardGoalReader: goalReader, DashboardPlanReader: func(_ context.Context, id string) (*plan.Plan, error) {
		return plan.NewStore(filepath.Join(r.home, "plans"), id).Load()
	}, CoordStore: r.Coordination, Canvas: r.Canvas, Config: r.cfg, SessionStore: store, SessionRuntimeFactory: r.BuildSession, SkillsRegistry: skillRegistry, DB: r.database, TasksStore: &api.TasksStoreAdapter{S: r.Tasks}, LearningStore: r.Learning, CostTracker: r.Cost, MemoEngine: r.Memo, FeatureManager: featureMgr, ExtensionsManager: extensionsMgr, ReadCache: speculative.NewReadCache(speculative.Options{}), SyncManager: syncManager, HotReloadBus: hotReloadBus}
}

// sessionTasks keeps normal agent tools within their owning session. The
// explicit cross-session sidebar API remains available to the human user.
type sessionTasks struct {
	store *tasks.Store
	id    string
}

func (s *sessionTasks) Open(id, intent string) (*tasks.Task, error) {
	if id != s.id {
		return nil, fmt.Errorf("tasks: session mismatch")
	}
	return s.store.Open(s.id, intent)
}
func (s *sessionTasks) Get(id string) (*tasks.Task, error) {
	task, err := s.store.Get(id)
	if err != nil {
		return nil, err
	}
	if task == nil || task.SessionID != s.id {
		return nil, fmt.Errorf("tasks: task not found in session")
	}
	return task, nil
}
func (s *sessionTasks) SetStatus(id string, status tasks.Status) (*tasks.Task, error) {
	if _, err := s.Get(id); err != nil {
		return nil, err
	}
	return s.store.SetStatus(id, status)
}
func (s *sessionTasks) LinkCommit(id, sha string) (*tasks.Task, error) {
	if _, err := s.Get(id); err != nil {
		return nil, err
	}
	return s.store.LinkCommit(id, sha)
}
func (s *sessionTasks) AppendNote(id, note string) (*tasks.Task, error) {
	if _, err := s.Get(id); err != nil {
		return nil, err
	}
	return s.store.AppendNote(id, note)
}
func (s *sessionTasks) All() ([]*tasks.Task, error) {
	all, err := s.store.All()
	if err != nil {
		return nil, err
	}
	out := []*tasks.Task{}
	for _, task := range all {
		if task.SessionID == s.id {
			out = append(out, task)
		}
	}
	return out, nil
}
func (s *sessionTasks) OpenTasks() ([]*tasks.Task, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	out := []*tasks.Task{}
	for _, task := range all {
		if !task.IsTerminal() {
			out = append(out, task)
		}
	}
	return out, nil
}

type sessionAlarms struct {
	gateway tools.AlarmGateway
	id      string
}

func (s *sessionAlarms) Set(alarm *automation.Alarm) error {
	if alarm == nil || alarm.SessionID != s.id {
		return fmt.Errorf("alarm: session mismatch")
	}
	return s.gateway.Set(alarm)
}
func (s *sessionAlarms) List() []*automation.Alarm {
	out := []*automation.Alarm{}
	for _, alarm := range s.gateway.List() {
		if alarm.SessionID == s.id {
			out = append(out, alarm)
		}
	}
	return out
}
func (s *sessionAlarms) Cancel(id string) bool {
	for _, alarm := range s.List() {
		if alarm.ID == id {
			return s.gateway.Cancel(id)
		}
	}
	return false
}
