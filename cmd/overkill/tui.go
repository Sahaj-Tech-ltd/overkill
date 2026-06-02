package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/browser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/browser/devbrowser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
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
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tasks"
	termpkg "github.com/Sahaj-Tech-ltd/overkill/internal/term"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	imagegen "github.com/Sahaj-Tech-ltd/overkill/internal/tools/imagegen"
	messaging "github.com/Sahaj-Tech-ltd/overkill/internal/tools/messaging"
	ttspkg "github.com/Sahaj-Tech-ltd/overkill/internal/tools/tts"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"

	"github.com/spf13/cobra"
	"sync"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the Ink terminal UI",
	RunE:  runInkTUI,
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

// runInkTUI starts the JSON-RPC API server on a random port, then launches
// the Ink TUI frontend.
func runInkTUI(cmd *cobra.Command, args []string) error {
	// P1: term background probe — detect dark/light mode before launching TUI.
	if dark, err := termProbeBackground(); err == nil {
		if dark {
			os.Setenv("OVERKILL_THEME", "dark")
		} else {
			os.Setenv("OVERKILL_THEME", "light")
		}
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return fmt.Errorf("can't find repo root: %w", err)
	}

	loadedCfg := cfg
	if loadedCfg == nil {
		loadedCfg = config.Default()
	}

	// Resolve database connection string.
	connString := loadedCfg.DatabaseURL
	if connString == "" {
		connString = os.Getenv("DATABASE_URL")
	}

	// Open Postgres for session and learning stores via internal/db.
	database, err := db.Open(connString)
	defer database.Close()
	if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		return fmt.Errorf("db migrate: %w", err)
	}

	// Session store (Postgres).
	sstore := session.NewPostgresStore(database)

	cwd, _ := os.Getwd()

	homeDir, _ := config.ConfigDir()

	// Playbooks: ACE-style evolving playbook store (§8.2 Phase 5 #6).
	playbookStore := playbooks.NewStore(filepath.Join(homeDir, "playbooks"))

	// AutoCommitter: religious commits per stage (§4.8).
	autoCommitter := automation.NewAutoCommitter(cwd, nil, nil)

	// Wire sub-agent system: router, registry, manager.
	subagentInfra := setupSubagentSystem(loadedCfg)

	// LSP manager — best-effort; skip if no servers configured.
	lspMgr := lsp.NewManager(loadedCfg.LSP, cwd)
	lspAdapter := newLSPManagerAdapter(lspMgr)

	// CheckpointManager — best-effort; skip if directory cannot be created.
	var checkpointMgr *checkpoint.Manager
	if cm, cerr := checkpoint.NewManager(filepath.Join(homeDir, "checkpoints"), 5); cerr == nil {
		checkpointMgr = cm
	} else {
		log.Printf("[wiring] checkpoint manager unavailable: %v", cerr)
	}

	// TagsManager — best-effort; skip if store cannot be opened.
	var tagsMgr *tags.Manager
	if tm, terr := tags.NewManager(filepath.Join(homeDir, "tags")); terr == nil {
		tagsMgr = tm
	} else {
		log.Printf("[wiring] tags manager unavailable: %v", terr)
	}

	// PlanQuerier — no error return; always succeeds.
	planStore := plan.NewStore(filepath.Join(homeDir, "plans"), "")

	// LearningsQuerier — JSONL-backed learning store (§6.2 self-learning loop).
	learningsStore := plan.NewLearningsStore(filepath.Join(homeDir, "learnings"))

	// LearnRecorder — in-session skill-suggestion trigger (§6.2).
	learnTrigger := skills.NewLearnTrigger(0, nil)

	// GoalQuerier — Postgres-backed; best-effort.
	var goalQuerier tools.GoalQuerier
	if gs, gsErr := agent.NewGoalStore(database); gsErr == nil {
		goalQuerier = tools.NewSessionGoalQuerier(goalStoreAdapter{gs}, func() string { return "" })
	} else {
		log.Printf("[wiring] goal store unavailable: %v", gsErr)
	}

	// ── TUI Ask-User Bridge ──────────────────────────────────────────
	// Shared channel-based bridge: the ask_user tool pushes questions
	// here, blocks until the TUI delivers an answer via the API server.
	type askUserResp struct {
		text   string
		index  int
		cancel bool
	}
	type askUserReq struct {
		prompt  string
		choices []string
		ch      chan askUserResp
	}
	type tuiAskBridge struct {
		mu      sync.Mutex
		pending *askUserReq
	}
	tuiBridge := &tuiAskBridge{}
	askUserBridge := tools.AskUserBridge(func(ctx context.Context, prompt string, choices []string) (string, int, bool) {
		req := &askUserReq{prompt: prompt, choices: choices, ch: make(chan askUserResp, 1)}
		tuiBridge.mu.Lock()
		tuiBridge.pending = req
		tuiBridge.mu.Unlock()
		defer func() {
			tuiBridge.mu.Lock()
			tuiBridge.pending = nil
			tuiBridge.mu.Unlock()
		}()
		select {
		case resp := <-req.ch:
			return resp.text, resp.index, resp.cancel
		case <-ctx.Done():
			return "", -1, true
		}
	})

	// TasksStore — cross-session task graph (§8.3 Phase 5 #2).
	tasksStore := tasks.NewStore(filepath.Join(homeDir, "tasks"))

	// SegmentsStore — labeled codebase slices (§8.2 Phase 5 #3 MemAgent).
	segmentsStore := memory.NewSegmentStore(filepath.Join(homeDir, "segments"), cwd)

	// StandingOrdersStore — persistent standing orders (§7.1).
	var standingOrdersStore tools.StandingOrdersStore
	if so, soErr := automation.NewOrdersFile(filepath.Join(homeDir, "standing-orders.jsonl")); soErr == nil {
		standingOrdersStore = so
	} else {
		log.Printf("[wiring] standing orders store unavailable: %v", soErr)
	}

	// FailHypoQuerier — failed-hypothesis store for avoiding known-dead paths.
	failHypoStore := journal.NewFailedHypothesisStore(filepath.Join(homeDir, "failhypo"))

	// AutomationLister — SOP store; best-effort (requires Postgres).
	var automationLister tools.AutomationLister
	if sops, sopsErr := automation.NewPostgresSOPStore(database); sopsErr == nil {
		automationLister = sops
	} else {
		log.Printf("[wiring] automation lister unavailable: %v", sopsErr)
	}

	// CronLister — cron job store; best-effort (requires Postgres).
	var cronLister tools.CronLister
	if jobs, jobsErr := cron.NewPostgresJobStore(database); jobsErr == nil {
		cronLister = jobs
	} else {
		log.Printf("[wiring] cron lister unavailable: %v", jobsErr)
	}

	// FlightRecorder — query-only journal reader (reads all sessions).
	flightRecorder := journal.NewFlightRecorder(filepath.Join(homeDir, "journal"), "")

	// ProjectRootResolver — returns the repo root for arch/glossary tools.
	projectRootFn := tools.ProjectRootResolver(func() string { return repoRoot })

	// Browser config — wired from TOML same as CLI path (run.go).
	browserOpts := browser.Options{}
	if loadedCfg != nil && loadedCfg.Browser.Enabled {
		browserOpts = browser.Options{
			Headless:   loadedCfg.Browser.Headless,
			ChromePath: loadedCfg.Browser.ChromePath,
			UserAgent:  loadedCfg.Browser.UserAgent,
		}
	}
	browserPolicy := tools.BrowserHostPolicy{
		Allowed: loadedCfg.Browser.AllowedHosts,
		Blocked: loadedCfg.Browser.BlockedHosts,
	}

	tuiDeps := tools.FactoryDeps{
		CWD:                 cwd,
		PlaybooksStore:      playbookStore,
		AutoCommitter:       autoCommitter,
		CheckpointManager:   checkpointMgr,
		CheckpointSessionFn: func() string { return "" },
		TagsManager:         tagsMgr,
		PlanQuerier:         planStore,
		ExtraTools: []tools.Tool{
			ttspkg.New(loadedCfg.TTS),
			messaging.New(loadedCfg.Gateways),
			imagegen.New(loadedCfg.ImageGen),
		},
		SubagentManager: subagentInfra.Manager,
		// PCA-9: RegressionBank — persisted behavioral regression tests.
		// Falls back to in-memory store if Postgres setup fails.
		RegressionBank: func() *walls.RegressionBank {
			if store, err := walls.NewPostgresRegressionStore(database); err == nil {
				return walls.NewRegressionBank(store, nil)
			}
			return walls.NewRegressionBank(walls.NewMemRegressionStore(), nil)
		}(),
		// PCA-10: MultimodalRegistry — file content extraction (PDF, DOCX, audio, images).
		MultimodalRegistry: multimodal.DefaultRegistry(nil),
		// LSP querier + manager.
		LSPQuerier: lspAdapter,
		LSPManager: lspMgr,
		// Pipeline runner — zero-value Config uses defaults.
		PipelineRunner: &pipelineRunnerAdapter{exec: pipeline.NewExecutor(pipeline.Config{})},
		// BrowserManager — full Playwright-equivalent browser.
		BrowserManager: browser.NewManager(browserOpts),
		BrowserPolicy:  browserPolicy,
		// DevBrowserManager — sandboxed AI-safe browser.
		DevBrowserManager: devbrowser.NewManager(),
		// ArchitectureWall — enforces import/layer rules.
		ArchitectureWall: walls.NewArchitectureWall(walls.ArchitectureConfig{}),
		// OuroborosWall — adversarial code review wall (§6.5).
		// Reads from config; creates a real wall when enabled, disabled otherwise.
		OuroborosWall: func() *walls.OuroborosWall {
			if loadedCfg != nil && loadedCfg.Ouroboros.Enabled {
				ouroCfg := loadedCfg.Ouroboros
				ouroKey := ouroCfg.APIKey
				if ouroKey == "" {
					ouroKey = os.Getenv(strings.ToUpper(ouroCfg.Provider) + "_API_KEY")
				}
				if ouroKey != "" && ouroCfg.Provider != "" {
					ouroProv, err := providers.NewProvider(providers.FactoryConfig{
						Name:    ouroCfg.Provider,
						Type:    ouroCfg.Provider,
						APIKey:  ouroKey,
						BaseURL: ouroCfg.BaseURL,
					})
					if err == nil {
						return walls.NewOuroborosWall(walls.OuroborosConfig{
							Enabled:      true,
							Provider:     ouroProv,
							Model:        ouroCfg.Model,
							StrictMode:   ouroCfg.StrictMode,
							SystemPrompt: ouroCfg.SystemPrompt,
						})
					}
					// Log the failure, but sanitize the error message — it may
					// contain the API key if the provider constructor leaks it.
					errMsg := err.Error()
					if ouroKey != "" {
						errMsg = strings.ReplaceAll(errMsg, ouroKey, "***")
					}
					log.Printf("tui: Ouroboros provider creation failed for '%s': %s", ouroCfg.Provider, errMsg)
				}
			}
			return walls.NewOuroborosWall(walls.OuroborosConfig{})
		}(),
		// Learnings: JSONL-backed querier + in-session skill-suggestion trigger.
		LearningsQuerier: learningsStore,
		LearnRecorder:    learnTrigger,
		// GoalQuerier — Postgres-backed; nil when database is unavailable.
		GoalQuerier: goalQuerier,
		// ── Newly wired fields ────────────────────────────────────
		AskUserBridge:       askUserBridge,
		TasksStore:          tasksStore,
		SessionIDProvider:   sessIDProvider(func() string { return "" }),
		SegmentsStore:       segmentsStore,
		StandingOrdersStore: standingOrdersStore,
		AutomationLister:    automationLister,
		CronLister:          cronLister,
		FailHypoQuerier:     failHypoStore,
		JournalQuerier:      flightRecorder,
		JournalReader:       flightRecorder,
		ProjectRootResolver: projectRootFn,
		AlarmSessionFn:      func() string { return "" },
		IntrospectDir:       filepath.Join(homeDir, "introspect"),
		SkillExtractDir:     filepath.Join(homeDir, "skills_extract"),
	}
	// VisionDescriber — best-effort; requires ANTHROPIC_API_KEY.
	if visionKey := os.Getenv("ANTHROPIC_API_KEY"); visionKey != "" {
		tuiDeps.VisionDescriber = vision.NewAnthropic(visionKey, "claude-opus-4-5")
	}

	// AlarmGateway — best-effort; skip if daemon socket is unavailable.
	if gw, err := newDaemonAlarmGateway(); err == nil {
		tuiDeps.AlarmGateway = gw
	} else {
		log.Printf("[wiring] alarm gateway unavailable (is the daemon running?): %v", err)
	}

	reg := tools.NewDefaultRegistry(tuiDeps)

	// Learning-from-corrections store (§6.5).
	var learningStore *learning.Store
	if database != nil {
		if ls, err := learning.NewStore(database, 1000); err == nil {
			learningStore = ls
		}
	}

	// Memo the Elephant — thinking indicator phrase engine.
	memoEngine := personality.NewMemoEngine(database)

	// P2: speculative read cache for the TUI path.
	readCache := speculative.NewReadCache(speculative.Options{})

	// Best-effort sync manager — only when the user enabled it in config.
	// Mirrors the CLI path in run.go.
	var syncMgr *syncpkg.Manager
	if loadedCfg != nil && loadedCfg.Sync.AutoPush && loadedCfg.Sync.Backend != "" {
		if be, berr := syncpkg.NewBackend(loadedCfg.Sync); berr == nil && be != nil {
			syncMgr = syncpkg.NewManager(sstore, be)
		}
	}

	// Cost tracker — powers session.usage RPC and the /usage command.
	var costTracker cost.Tracker
	if ct, cerr := cost.NewPostgresTracker(database, loadedCfg.Cost); cerr == nil {
		costTracker = ct
	} else {
		log.Printf("cost tracker init failed: %v (usage tracking disabled)", cerr)
	}

	apiServer := api.NewServer(api.ServerConfig{
		Config:            loadedCfg,
		SessionStore:      sstore,
		Tools:             reg,
		LearningStore:     learningStore,
		FeatureManager:    featureMgr,
		ExtensionsManager: extensionsMgr,
		ReadCache:         readCache,
		MemoEngine:        memoEngine,
		SyncManager:       syncMgr,
		HotReloadBus:      hotReloadBus,
		SubagentManager:   &subagentManagerAdapter{mgr: subagentInfra.Manager},
		CostTracker:       costTracker,
		TasksStore:        &api.TasksStoreAdapter{S: tasksStore},
	})

	// Start API server in background.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- apiServer.Start(ctx)
	}()

	// Wait for the server to bind a port using a ready-channel retry loop
	// instead of a fixed sleep which races on slow systems.
	apiReadyCh := make(chan string, 1)
	go func() {
		for i := 0; i < 50; i++ { // 50 * 10ms = 500ms max wait
			addr := apiServer.Addr()
			if addr != "" && addr != "http://localhost:0" {
				apiReadyCh <- addr
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	var apiAddr string
	select {
	case apiAddr = <-apiReadyCh:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("API server failed to bind a port within timeout")
	}
	log.Printf("API ready at %s", apiAddr)

	// Launch the Ink TUI.
	tuiPath := filepath.Join(repoRoot, "tui")
	tuiProc := exec.Command("npm", "start")
	tuiProc.Dir = tuiPath
	tuiProc.Env = append(os.Environ(),
		"OVERKILL_API_URL="+apiAddr,
		"OVERKILL_API_PORT="+apiPort(apiAddr),
		"FORCE_COLOR=1",
	)
	tuiProc.Stdin = os.Stdin
	tuiProc.Stdout = os.Stdout
	tuiProc.Stderr = os.Stderr

	if err := tuiProc.Start(); err != nil {
		cancel()
		return fmt.Errorf("launching Ink TUI: %w (did you run 'npm install' in %s?)", err, tuiPath)
	}

	// Wait for TUI or signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- tuiProc.Wait()
	}()

	select {
	case err := <-doneCh:
		if err != nil {
			log.Printf("TUI exited: %v", err)
		}
	case sig := <-sigCh:
		log.Printf("Received %v, shutting down...", sig)
		tuiProc.Process.Signal(sig)
		<-doneCh
	}

	cancel()
	<-errCh // wait for API server to shut down
	return nil
}

// findRepoRoot walks up from cwd looking for go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return os.Getwd()
}

// apiPort extracts the port number from an address like "127.0.0.1:8420".
func apiPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return port
}
func termProbeBackground() (bool, error) {
	return termpkg.QueryBackground(200 * time.Millisecond)
}
