package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Sahaj-Tech-ltd/overkill/bridge"
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automemory"
	"github.com/Sahaj-Tech-ltd/overkill/internal/browser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/browser/devbrowser"
	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/credit"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cron"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/drift"
	"github.com/Sahaj-Tech-ltd/overkill/internal/events"
	eventsinks "github.com/Sahaj-Tech-ltd/overkill/internal/events/sinks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hotreload"
	"github.com/Sahaj-Tech-ltd/overkill/internal/input"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/lats"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/lsp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/memory"
	"github.com/Sahaj-Tech-ltd/overkill/internal/multimodal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/pipeline"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
	"github.com/Sahaj-Tech-ltd/overkill/internal/playbooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/rewriter"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/speculative"
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tasks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	imagegen "github.com/Sahaj-Tech-ltd/overkill/internal/tools/imagegen"
	messaging "github.com/Sahaj-Tech-ltd/overkill/internal/tools/messaging"
	ttspkg "github.com/Sahaj-Tech-ltd/overkill/internal/tools/tts"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
	"github.com/Sahaj-Tech-ltd/overkill/internal/walls"
)

var (
	modelOverride    string
	providerOverride string
	noPersonality    bool
	noBoot           bool
	latsEnabled      bool
	outputFormat     string
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the agent loop",
	RunE:  runAgent,
}

func runAgent(cmd *cobra.Command, args []string) error {
	providerCfg, modelName := resolveProvider()

	if providerCfg == nil {
		fmt.Printf("%s✗ No provider configured.%s\n", colorRed, colorReset)
		fmt.Println()
		fmt.Println("OpenCode-style: set an API key env var and overkill auto-detects it:")
		fmt.Println()
		fmt.Println("  export OPENAI_API_KEY=sk-...")
		fmt.Println("  export ANTHROPIC_API_KEY=sk-ant-...")
		fmt.Println("  export GEMINI_API_KEY=...")
		fmt.Println("  export DEEPSEEK_API_KEY=sk-...")
		fmt.Println()
		fmt.Println("Or add providers manually:")
		fmt.Println("  overkill config init")
		fmt.Println("  # edit ~/.overkill/config.toml")
		return fmt.Errorf("no provider configured")
	}

	apiKey := providerCfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(providerEnvVar(providerCfg.Name))
	}

	provider, err := providers.NewProvider(providers.FactoryConfig{
		Name:    providerCfg.Name,
		Type:    providerCfg.Type,
		APIKey:  apiKey,
		BaseURL: providerCfg.BaseURL,
		Headers: providerCfg.Headers,
	})
	if err != nil {
		return fmt.Errorf("creating provider: %w", err)
	}

	// JSONL output: wrap the provider so every Complete call emits a
	// JSONL record to stdout. The wrapper also exposes EmitUser so we
	// can write the initial user message as a JSONL record.
	var jl *jsonlProvider
	if outputFormat == "jsonl" {
		jl = newJSONLProvider(provider, providerCfg.Name, os.Stdout)
		provider = jl
	}

	cwd, _ := os.Getwd()
	homeDir, err := config.ConfigDir()
	if err != nil {
		homeDir = ""
	}

	// Playbooks: ACE-style evolving playbook store (§8.2 Phase 5 #6).
	playbookStore := playbooks.NewStore(filepath.Join(homeDir, "playbooks"))

	// AutoCommitter: religious commits per stage (§4.8).
	// All stages disabled by default — the user enables via config or
	// the agent enables specific stages via autocommit_stage tool.
	autoCommitter := automation.NewAutoCommitter(cwd, nil, nil)

	// Wire sub-agent system: router, registry, manager.
	subagentInfra := setupSubagentSystem(cfg)

	// LSP manager — best-effort; skip if no servers configured.
	lspMgr := lsp.NewManager(cfg.LSP, cwd)
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

	// Resolve database connection string early so goal/learning stores can be
	// created before the toolDeps literal. The same connString is reused later
	// for the learning-from-corrections store and memory orchestrator.
	earlyConnString := os.Getenv("DATABASE_URL")
	if earlyConnString == "" && cfg != nil {
		earlyConnString = cfg.DatabaseURL
	}

	// PlanQuerier — no error return; always succeeds.
	planStore := plan.NewStore(filepath.Join(homeDir, "plans"), "")

	// LearningsQuerier — JSONL-backed learning store (§6.2 self-learning loop).
	learningsStore := plan.NewLearningsStore(filepath.Join(homeDir, "learnings"))

	// LearnRecorder — in-session skill-suggestion trigger (§6.2).
	learnTrigger := skills.NewLearnTrigger(0, nil)

	// GoalQuerier — Postgres-backed goal store; best-effort.
	var goalQuerier tools.GoalQuerier
	if earlyConnString != "" {
		if goalDB, gdbErr := db.Open(earlyConnString); gdbErr == nil {
			if gs, gsErr := agent.NewGoalStore(goalDB); gsErr == nil {
				goalQuerier = tools.NewSessionGoalQuerier(goalStoreAdapter{gs}, func() string { return "" })
				defer goalDB.Close()
			} else {
				goalDB.Close()
				log.Printf("[wiring] goal store unavailable: %v", gsErr)
			}
		} else {
			log.Printf("[wiring] goal db unavailable: %v", gdbErr)
		}
	}

	// Active session ID — closure-captured by CheckpointSessionFn /
	// SessionIDProvider; set after agent creation so tools resolve to the
	// live session.
	var activeSessionID string

	// TasksStore — file-backed task tracker (§8.3).
	taskStore := tasks.NewStore(filepath.Join(homeDir, "tasks"))

	// SegmentsStore — code region segmenter for granular context.
	segStore := memory.NewSegmentStore(filepath.Join(homeDir, "segments"), cwd)

	// StandingOrdersStore — persistent standing orders (§7.2).
	soStore, soErr := automation.NewOrdersFile(filepath.Join(homeDir, "standing-orders.jsonl"))
	if soErr != nil {
		log.Printf("[wiring] standing orders store unavailable: %v", soErr)
		soStore = nil
	}

	// FailHypoQuerier — failed hypothesis store for avoiding repeated mistakes.
	fhStore := journal.NewFailedHypothesisStore(filepath.Join(homeDir, "failed_hypotheses"))

	// JournalQuerier + JournalReader — FlightRecorder implements both.
	flightRec := journal.NewFlightRecorder(filepath.Join(homeDir, "journal"), "")

	// AutomationLister + CronLister — Postgres-backed; best-effort.
	var sopLister tools.AutomationLister
	var cronLister tools.CronLister
	if earlyConnString != "" {
		if pgDB, pgErr := db.Open(earlyConnString); pgErr == nil {
			if s, sErr := automation.NewPostgresSOPStore(pgDB); sErr == nil {
				sopLister = s
			} else {
				log.Printf("[wiring] SOP store unavailable: %v", sErr)
			}
			if c, cErr := cron.NewPostgresJobStore(pgDB); cErr == nil {
				cronLister = c
			} else {
				log.Printf("[wiring] cron store unavailable: %v", cErr)
			}
			if sopLister == nil && cronLister == nil {
				pgDB.Close()
			}
			// pgDB intentionally kept open when at least one store succeeded;
			// closes at process exit.
		} else {
			log.Printf("[wiring] automation/cron db unavailable: %v", pgErr)
		}
	}

	// ── Share uploader (TODO) ──
	// share.NewUploader(cfg.Share) exists at internal/share/upload.go:24
	// but there is no share Tool registered in the tools package. The
	// uploader is only used by the `overkill share <sessionID>` CLI
	// command (share_cmd.go). Wire a share tool here when one is created.
	browserOpts := browser.Options{}
	if cfg != nil && cfg.Browser.Enabled {
		browserOpts = browser.Options{
			Headless:   cfg.Browser.Headless,
			ChromePath: cfg.Browser.ChromePath,
			UserAgent:  cfg.Browser.UserAgent,
		}
	}
	// Browser host policy: AllowedHosts / BlockedHosts from config.
	// TODO: BrowserConfig.Timeout is not yet wired — browser.Options
	// does not expose a timeout field; the chromedp layer uses its own
	// default timeouts. Wire when browser.Manager supports it.
	browserPolicy := tools.BrowserHostPolicy{
		Allowed: cfg.Browser.AllowedHosts,
		Blocked: cfg.Browser.BlockedHosts,
	}

	toolDeps := tools.FactoryDeps{
		CWD:                 cwd,
		PlaybooksStore:      playbookStore,
		AutoCommitter:       autoCommitter,
		CheckpointManager:   checkpointMgr,
		CheckpointSessionFn: func() string { return activeSessionID },
		TagsManager:         tagsMgr,
		PlanQuerier:         planStore,
		SubagentManager:     subagentInfra.Manager,
		ExtraTools: []tools.Tool{
			ttspkg.New(cfg.TTS),
			messaging.New(cfg.Gateways),
			imagegen.New(cfg.ImageGen),
		},
		// PCA-9: RegressionBank — persisted behavioral regression tests.
		RegressionBank: walls.NewRegressionBank(walls.NewMemRegressionStore(), nil),
		// PCA-10: MultimodalRegistry — file content extraction (PDF, DOCX, audio, images).
		MultimodalRegistry: multimodal.DefaultRegistry(nil),
		// LSP querier + manager.
		LSPQuerier: lspAdapter,
		LSPManager: lspMgr,
		// Pipeline runner — zero-value Config uses defaults; provider wired below.
		PipelineRunner: &pipelineRunnerAdapter{exec: pipeline.NewExecutor(pipeline.Config{})},
		// Learnings: JSONL-backed querier + in-session skill-suggestion trigger.
		LearningsQuerier: learningsStore,
		LearnRecorder:    learnTrigger,
		// GoalQuerier — Postgres-backed; nil when DATABASE_URL is unset.
		GoalQuerier: goalQuerier,
		// Tasks + SessionID (§8.3).
		TasksStore:        taskStore,
		SessionIDProvider: sessIDProvider(func() string { return activeSessionID }),
		// Segments + Standing Orders (§7.2).
		SegmentsStore:       segStore,
		StandingOrdersStore: soStore,
		// Journal querier + reader (FlightRecorder implements both).
		JournalQuerier: flightRec,
		JournalReader:  flightRec,
		// Automation + Cron listers (Postgres-backed; nil when DB is absent).
		AutomationLister: sopLister,
		CronLister:       cronLister,
		// FailHypo — failed hypothesis store.
		FailHypoQuerier: fhStore,
		// ProjectRootResolver — returns CWD for arch/glossary reads.
		ProjectRootResolver: func() string { return cwd },
		// AskUserBridge — CLI stub that cancels (no interactive TUI in CLI mode).
		AskUserBridge: func(ctx context.Context, prompt string, choices []string) (string, int, bool) {
			return "", -1, true
		},
		// BrowserManager — full Playwright-equivalent browser.
		BrowserManager: browser.NewManager(browserOpts),
		BrowserPolicy:  browserPolicy,
		// DevBrowserManager — sandboxed AI-safe browser.
		DevBrowserManager: devbrowser.NewManager(),
		// ArchitectureWall — enforces import/layer rules.
		ArchitectureWall: walls.NewArchitectureWall(walls.ArchitectureConfig{}),
		// OuroborosWall — detects self-referential loops. Wired from config
		// when cfg.Ouroboros.Enabled is true; uses a separate provider so
		// the review model is independent of the main agent.
		OuroborosWall: func() *walls.OuroborosWall {
			if cfg != nil && cfg.Ouroboros.Enabled {
				ouroCfg := cfg.Ouroboros
				ouroKey := ouroCfg.APIKey
				if ouroKey == "" {
					ouroKey = os.Getenv(providerEnvVar(ouroCfg.Provider))
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
							Enabled:    true,
							Provider:   ouroProv,
							Model:      ouroCfg.Model,
							StrictMode: ouroCfg.StrictMode,
						})
					}
				}
			}
			return walls.NewOuroborosWall(walls.OuroborosConfig{})
		}(),
		// Introspect / Skill Extract directories.
		IntrospectDir:   filepath.Join(homeDir, "introspection"),
		SkillExtractDir: filepath.Join(homeDir, "skills"),
	}
	// VisionDescriber — config-driven with env-var fallback.
	if cfg != nil && cfg.Vision.Enabled {
		visionKey := cfg.Vision.APIKey
		if visionKey == "" {
			visionKey = os.Getenv("ANTHROPIC_API_KEY")
		}
		visionModel := cfg.Vision.Model
		if visionModel == "" {
			visionModel = "claude-opus-4-5"
		}
		if visionKey != "" {
			toolDeps.VisionDescriber = vision.NewAnthropic(visionKey, visionModel)
		}
	} else if visionKey := os.Getenv("ANTHROPIC_API_KEY"); visionKey != "" {
		// Legacy env-var-only fallback when config is not present.
		toolDeps.VisionDescriber = vision.NewAnthropic(visionKey, "claude-opus-4-5")
	}
	// AlarmGateway — best-effort; skip if daemon socket is unavailable.
	// Must be wired before NewDefaultRegistry so the factory's nil-check registers the tools.
	if gw, err := newDaemonAlarmGateway(); err == nil {
		toolDeps.AlarmGateway = gw
		toolDeps.AlarmSessionFn = func() string { return activeSessionID }
	} else {
		log.Printf("[wiring] alarm gateway unavailable (is the daemon running?): %v", err)
	}
	toolReg := tools.NewDefaultRegistry(toolDeps)

	// ── Security: PermissionManager (interactive allow/deny tracking) ──
	permMgr := security.NewPermissionManager()

	agentCfg := agent.Config{
		Provider:    provider,
		Tools:       toolReg,
		Compressors: tools.NewCompressorRegistry(),
		Hooks:       hooks.NewRegistry(),
		Scanners: []security.Scanner{
			security.NewCommandScanner(
				security.WithProjectPath(cwd),
				security.WithPermissionManager(permMgr),
				security.WithExtraDenyPatterns(cfg.Security.DenyPatterns),
				security.WithForbiddenPaths(cfg.Security.ForbiddenPaths),
				security.WithMaxCommandLen(cfg.Security.MaxCommandLen),
			),
			security.NewInjectionScanner(),
		},
		Tokenizer:    tokenizer.NewEstimator(),
		Steering:     agent.NewSteeringQueue(agent.SteeringDrainAll),
		Model:        modelName,
		MaxTokens:    200000,
		SystemPrompt: buildSystemPrompt(cfg),
		MaxSteps:     cfg.Agent.MaxTurns,
	}

	// Wiring nil-seat hook: logs every nil pointer/interface field in
	// FactoryDeps at session start so dead wiring is visible in daemon
	// logs instead of silently degrading. Part of the PCA-prevention
	// strategy (wiring.md §11, Fix 1).
	agentCfg.Hooks.Register(hooks.Hook{
		Name:  "wiring.nil-seat-report",
		Point: hooks.OnSessionStart,
		Fn: func(ctx context.Context, _ hooks.Event) (context.Context, error) {
			v := reflect.ValueOf(toolDeps)
			t := v.Type()
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if (f.Kind() == reflect.Ptr || f.Kind() == reflect.Interface) && f.IsNil() {
					log.Printf("[WIRING] %s is nil — tools depending on it are disabled", t.Field(i).Name)
				}
			}
			return ctx, nil
		},
	})

	a := agent.New(agentCfg)

	// Wire autonomy level from config when set.
	if cfg.Security.AutonomyLevel != "" {
		a.SetAutoMode(cfg.Security.AutonomyLevel)
	}

	// Wire the active session ID back into the closure-captured variable
	// so tools that depend on SessionIDProvider / CheckpointSessionFn
	// resolve to the live session.
	activeSessionID = a.SessionID()

	// BeatRecorder — relationship arc tracking.
	a.SetBeatRecorder(&beatRecorderAdapter{tracker: personality.NewRelationshipTracker()})

	// ── Security: PrivilegeGate (read/write/admin tool privilege modes) ──
	privGate := security.NewPrivilegeGate(security.ModeWriter)
	a.SetPrivilegeGate(privGate)

	// ── Halluscan: hallucination scanner (Batch G3) ───────────────────────
	// Scans every assistant response for unverified backtick-quoted
	// identifiers and annotates them with [?] markers. Disabled via
	// OVERKILL_NO_HALLUSCAN=1.
	if hs := newHalluscanAdapter(); hs != nil {
		a.SetHallucinationScanner(hs)
	}

	// ── Drift Detection: live session drift monitoring ────────────────────
	// Same store path as post-hoc finalizeSession drift check, but this
	// wires a per-turn hook so the agent can detect behavioural shifts
	// while the user is still in the session (not just at exit).
	driftStore := drift.NewStore(filepath.Join(homeDir, "drift", "baseline.json"))
	agentCfg.Hooks.Register(hooks.Hook{
		Name:  "drift.per-turn-check",
		Point: hooks.AfterToolCall,
		Fn: func(ctx context.Context, _ hooks.Event) (context.Context, error) {
			toolCalls, errs, _, turns, _ := a.SessionMetrics()
			if turns == 0 {
				return ctx, nil
			}
			sample := map[drift.Metric]float64{
				drift.MetricToolCallsPerTurn: float64(toolCalls) / float64(turns),
				drift.MetricErrorRate:        float64(errs),
				drift.MetricSessionLength:    float64(turns),
			}
			baseline, loadErr := driftStore.Load()
			if loadErr != nil || baseline == nil {
				return ctx, nil
			}
			findings := baseline.Compare(sample, drift.CompareOptions{Threshold: 2.0})
			if len(findings) > 0 {
				log.Printf("drift: %s", drift.FormatFindings(findings))
			}
			baseline.Fold(sample)
			_ = driftStore.Save(baseline)
			return ctx, nil
		},
	})

	// Personality subsystem wiring (§4.16). Wire all five detectors into
	// the agent's user-input observer and personality provider so the
	// agent adapts its tone and behaviour across turns.
	if !noPersonality {
		// 1. BlindSpotDetector — tracks repeated task patterns and
		//    surfaces gentle heads-ups when the user is stuck on the
		//    same verb (fix, refactor, debug, …) across turns.
		blindSpot := personality.NewBlindSpotDetector()

		// 2. ColdStartManager — detects first-ever sessions and
		//    prompts the user for a communication-style baseline.
		var coldStart *personality.ColdStartManager
		if homeDir != "" {
			coldStart = personality.NewColdStartManager(filepath.Join(homeDir, "memories"))
		}

		// 3. TransparencyEngine — accumulates per-(task,model)
		//    failure counts and warns before retrying known-bad
		//    paths.
		transparency := personality.NewTransparencyEngine(modelName)

		// 4. StyleInferencer — two-layer communication style model
		//    (short-term flip per turn, long-term baseline drift).
		styleInfer := personality.NewStyleInferencer()

		// 5. FrustrationDetector — watches the user-input stream
		//    for ALL-CAPS shouting, repeated requests, profanity,
		//    and emphatic punctuation. No alert sink in the CLI
		//    path (nil = track silently; IsHot still works).
		frustration := personality.NewFrustrationDetector(nil, "")

		// Fan-out user-input observer: every user message feeds all
		// five detectors concurrently. Panics in any observer are
		// recovered by the agent, so a bug in one detector never
		// blocks the main loop.
		a.SetUserInputObserver(func(input string) {
			blindSpot.Observe(input)
			styleInfer.Observe(input)
			frustration.Observe(input)
		})

		// Personality provider: each turn the detectors can inject
		// context-sensitive directives into the system prompt.
		// cfg.Personality.Level controls intensity: "subtle" (default),
		// "witty", "full", or "off". Level "off" disables all personality
		// injection (but detectors still observe for analytics).
		a.SetPersonalityProvider(func() string {
			// Respect cfg.Personality.Level — "off" means no injection.
			if cfg != nil && cfg.Personality.Level == "off" {
				return ""
			}
			var parts []string

			// Blind-spot nudge when a pattern crosses threshold.
			if w := blindSpot.NextWarning(); w != "" {
				parts = append(parts, w)
			}

			// Transparency heads-up when a task type has failed
			// multiple times under the current model.
			if w := transparency.NextWarning(); w != "" {
				parts = append(parts, w)
			}

			// Frustration-is-hot: drop preamble, match urgency.
			if frustration.IsHot(5 * time.Minute) {
				parts = append(parts, "The user seems frustrated. Be concise, direct, and skip preamble. Match their urgency without panic.")
			}

			// Style-based guidance from the short-term inferencer.
			if style := styleInfer.Current(); style != nil {
				switch style.Communication {
				case personality.CommDirect:
					parts = append(parts, "Be direct and concise in your responses.")
				case personality.CommVerbose:
					parts = append(parts, "The user prefers detailed explanations and thoroughness.")
				case personality.CommContextual:
					parts = append(parts, "Provide context and reasoning alongside your answers.")
				}
			}

			return strings.Join(parts, "\n")
		})

		// Cold-start check: if this is the first session, print the
		// opening question so the user knows Overkill is learning
		// their style. The answer is handled naturally on the next
		// turn — we don't block the REPL.
		if coldStart != nil && coldStart.IsColdStart() {
			fmt.Printf("\n%s%s%s\n\n", colorYellow, coldStart.OpeningQuestion(), colorReset)
		}
	}

	// Wire the learning-from-corrections store (§6.5). This persists
	// user corrections so future turns benefit from past feedback.
	connString := os.Getenv("DATABASE_URL")
	if connString == "" && cfg != nil {
		connString = cfg.DatabaseURL
	}
	if connString != "" {
		// Open a shared DB connection for learning store and memory orchestrator.
		sharedDB, dbErr := db.Open(connString)
		if dbErr == nil {
			if ls, err := learning.NewStore(sharedDB, 1000); err == nil {
				a.SetLearningStore(ls)
			}
		}
	}

	// PCA-6: Memory orchestrator — always-on Postgres-backed memory store.
	// Optionally enriched with embeddings/semantic search when the Python
	// gRPC bridge is available (OVERKILL_BRIDGE_ADDR).
	if connString != "" {
		if memDB, dbErr := db.Open(connString); dbErr == nil {
			if memStore, msErr := memory.NewPostgresStore(memDB); msErr == nil {
				orch := memory.NewOrchestrator(memStore, provider, modelName)

				// Optional: attach bridge-based embeddings for semantic search.
				if bridgeAddr := os.Getenv("OVERKILL_BRIDGE_ADDR"); bridgeAddr != "" {
					if bc, bcErr := bridge.NewClient(bridgeAddr); bcErr == nil {
						adapter := memory.NewBridgeAdapter(bc)
						orch.AttachEmbeddings(adapter, memory.SemanticConfig{
							EmbedModel:      "text-embedding-3-small",
							SearchThreshold: 0.7,
						})
					}
				}

				a.SetMemoryRetriever(&memoryOrchRetriever{orch: orch})
				// Ownership: these close at process exit.
			} else {
				memDB.Close()
			}
		}
	}

	// Bookmark store: PostgreSQL-backed session bookmarks (§7.4).
	// Wired when a database connection is available; silently skipped
	// when DATABASE_URL is not set.
	if connString != "" {
		if bmDB, bmErr := db.Open(connString); bmErr == nil {
			if bs, bsErr := gateway.NewBookmarkStore(bmDB); bsErr == nil {
				a.SetSessionBookmarkStore(&bookmarkStoreAdapter{store: bs})
				// bmDB closes at process exit alongside other DB connections.
				defer bmDB.Close()
			} else {
				bmDB.Close()
			}
		}
	}

	// ── AutoMemory: post-turn memory extraction to ~/.overkill/memory/ ──
	if homeDir, err := config.ConfigDir(); err == nil && homeDir != "" {
		autoMem := automemory.NewExtractor(homeDir)
		a.SetAutoMemory(autoMem)
	}

	// PCA-7: Prompt rewriter middleware ($4.10). When enabled the agent
	// pipes every user message through the rewriter before the model sees
	// it — stripping sycophancy, catching anti-patterns, and optionally
	// expanding ambiguous/complex prompts via LLM.
	if cfg != nil && cfg.Rewriter.Enabled {
		rwModel := cfg.Rewriter.Model
		if rwModel == "" {
			rwModel = modelName
		}
		rw := rewriter.NewLLMRewriter(provider, rwModel)
		// TODO: cfg.Rewriter.StripSycophancy — the SycophancyReducer is
		// always active in NewLLMRewriter. Add a configurable toggle.
		// TODO: cfg.Rewriter.AntiPatternGuard — no anti-pattern guard
		// exists yet. Wire when anti-pattern detection is implemented.
		// TODO: cfg.Rewriter.LLMRewrite — the LLM expansion path is
		// always active for ambiguous/complex prompts. Add a toggle to
		// skip the LLM call and fall through to passthrough instead.
		a.SetRewriter(rw)
	}

	// P0: context compaction — wire LCM-based compactor.
	if compactProv := provider; compactProv != nil {
		compactor := compaction.NewAgentCompactor(compactProv, tokenizer.NewEstimator(), 20)
		a.SetCompactor(compactor, true)
	}

	// P0: hotreload — wire config file watcher.
	if hotReloadBus != nil {
		homeDir, _ := config.ConfigDir()
		if homeDir != "" {
			userYAML := filepath.Join(homeDir, "user.yaml")
			if _, err := hotreload.WireAgent(context.Background(), hotReloadBus, a, userYAML, hotreload.DiscardReporter()); err != nil {
				log.Printf("hotreload: wire agent: %v", err)
			}
		}
	}

	// P0: input classifier — shell vs NL routing.
	a.SetInputClassifier(func(raw string) agent.InputKind {
		return agent.InputKind(input.Classify(raw))
	})

	// P1: events/sinks — completion event emitter.
	emit := events.NewEmitter(eventsinks.NewLogSink(log.Default()))
	a.SetCompletionEmitter(emit, nil)

	// P1: feature flags.
	if featureMgr != nil {
		a.SetFeatureManager(featureMgr)
	}

	// P2: speculative read cache + prefetcher.
	readCache := speculative.NewReadCache(speculative.Options{})
	a.SetReadCache(readCache)
	prefetcher := speculative.NewPrefetcher(readCache, 2, 64)
	prefetcher.Start(2)
	defer prefetcher.Stop()

	// P2: extensions manager.
	if extensionsMgr != nil {
		a.SetExtensionsManager(wrapExtensions(extensionsMgr))
	}

	fmt.Printf("overkill > %s %s\n", providerCfg.Name, modelName)
	fmt.Printf("Type a message, /help for commands, Ctrl+D to exit.\n\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Printf("\n%sShutting down...%s\n", colorYellow, colorReset)
		finalizeSession(a, providerCfg.Name, modelName)
		cancel()
		os.Exit(0)
	}()

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Printf("%s>%s ", colorGreen, colorReset)
		if !scanner.Scan() {
			fmt.Println("\nGoodbye.")
			finalizeSession(a, providerCfg.Name, modelName)
			return nil
		}

		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}

		// P0: classify input — route shell commands directly.
		if input.Classify(raw) == input.KindShell {
			fmt.Print("\r\033[K")
			fmt.Printf("%s$ %s%s\n", colorDim, raw, colorReset)
			// Execute shell command directly.
			runShellCommand(raw)
			continue
		}

		input := raw

		if strings.HasPrefix(input, "/") {
			parts := strings.Fields(input)
			switch strings.ToLower(parts[0]) {
			case "/exit", "/quit":
				fmt.Println("Goodbye.")
				finalizeSession(a, providerCfg.Name, modelName)
				return nil
			case "/help":
				fmt.Println("  /exit /quit  - quit")
				fmt.Println("  /help        - this message")
				fmt.Println("  /clear       - clear history")
				fmt.Println("  /history     - show history")
				fmt.Println("  /model <id>  - switch model")
			case "/clear":
				a.ClearHistory()
				fmt.Printf("%sHistory cleared.%s\n", colorGreen, colorReset)
			case "/history":
				for i, msg := range a.History() {
					fmt.Printf("  %d [%s] %s\n", i+1, msg.Role, truncate(msg.Content, 200))
				}
			case "/model":
				if len(parts) > 1 {
					a.SetModel(parts[1])
					fmt.Printf("%sModel: %s%s\n", colorGreen, parts[1], colorReset)
				} else {
					fmt.Printf("Model: %s\n", a.Model())
				}
			default:
				fmt.Printf("%sUnknown: %s%s\n", colorYellow, parts[0], colorReset)
			}
			continue
		}

		fmt.Print("\r\033[K")

		// JSONL: emit user message before the agent loop starts.
		if jl != nil {
			jl.EmitUser(input, 0)
		}

		var result *agent.RunResult
		var err error

		// P3: LATS — multi-branch tree search.
		if latsEnabled {
			branches := []lats.Branch{
				{ID: "direct", Approach: "direct solution"},
				{ID: "careful", Approach: "careful step-by-step analysis"},
			}
			runner := lats.RunnerFunc(func(ctx context.Context, branch lats.Branch, workdir string) (string, string, error) {
				r, e := a.Run(ctx, input)
				if e != nil {
					return "failed", "", e
				}
				return "completed", r.Response, nil
			})
			results, latsErr := lats.Race(context.Background(), branches, runner, nil, lats.Options{
				MaxBranches:      2,
				PerBranchTimeout: 5 * time.Minute,
				FallbackWorkdir:  cwd,
			}, nil)
			if latsErr == nil && len(results) > 0 {
				result = &agent.RunResult{Response: results[0].Response}
				fmt.Printf("%sLATS winner: %s (score %.2f)%s\n", colorDim, results[0].Branch.Approach, results[0].Score, colorReset)
			} else {
				result, err = a.Run(ctx, input)
			}
		} else {
			result, err = a.Run(ctx, input)
		}
		if err != nil {
			fmt.Printf("%s✗ %s%s\n", colorRed, err, colorReset)
			continue
		}

		if result.Blocked {
			fmt.Printf("%s✗ Blocked: %s%s\n", colorRed, result.BlockReason, colorReset)
		} else {
			fmt.Println(result.Response)
		}

		fmt.Printf("\n%s%d steps · %d tools · %d tokens%s\n\n",
			colorDim, result.Steps, result.ToolCalls, result.TotalTokens, colorReset)

		// Mirror TUI behaviour: optional non-blocking sync push after each
		// successful turn. Errors land on stderr so the user sees them
		// without breaking the REPL.
		syncpkg.AutoPushIfEnabled(cfg, nil, a.SessionID(), func(err error) {
			fmt.Fprintf(os.Stderr, "%ssync auto-push failed: %s%s\n", colorYellow, err.Error(), colorReset)
		})
	}
}

func resolveProvider() (*config.ProviderConfig, string) {
	// First: check config file
	if providerOverride != "" {
		for i := range cfg.Providers {
			if cfg.Providers[i].Name == providerOverride {
				if len(cfg.Providers[i].Models) == 0 {
					return &cfg.Providers[i], ""
				}
				return &cfg.Providers[i], cfg.Providers[i].Models[0].ID
			}
		}
	}
	if len(cfg.Providers) > 0 {
		p := &cfg.Providers[0]
		model := cfg.Agent.DefaultModel
		if model == "" && len(p.Models) > 0 {
			model = p.Models[0].ID
		}
		// If model is still empty, leave it — the provider API will
		// use its own default. Don't hardcode "gpt-4o" for providers
		// that don't support it.
		return p, model
	}

	// Second: auto-detect from env vars (like OpenCode)
	detected := detectProviderFromEnv()
	if detected != nil {
		return detected, ""
	}

	return nil, ""
}

var envProviders = []struct {
	name    string
	envVar  string
	typ     string
	baseURL string
}{
	{"openai", "OPENAI_API_KEY", "openai", "https://api.openai.com/v1"},
	{"anthropic", "ANTHROPIC_API_KEY", "anthropic", "https://api.anthropic.com"},
	{"gemini", "GEMINI_API_KEY", "gemini", "https://generativelanguage.googleapis.com/v1beta"},
	{"deepseek", "DEEPSEEK_API_KEY", "deepseek", "https://api.deepseek.com/v1"},
	{"openrouter", "OPENROUTER_API_KEY", "openrouter", "https://openrouter.ai/api/v1"},
	{"groq", "GROQ_API_KEY", "groq", "https://api.groq.com"},
	{"xai", "XAI_API_KEY", "xai", "https://api.x.ai"},
	{"mistral", "MISTRAL_API_KEY", "mistral", "https://api.mistral.ai/v1"},
	{"togetherai", "TOGETHERAI_API_KEY", "togetherai", "https://api.together.xyz/v1"},
	{"perplexity", "PERPLEXITY_API_KEY", "perplexity", "https://api.perplexity.ai"},
	{"deepinfra", "DEEPINFRA_API_KEY", "deepinfra", "https://api.deepinfra.com/v1/openai"},
	{"cerebras", "CEREBRAS_API_KEY", "cerebras", "https://api.cerebras.ai/v1"},
	{"fireworks", "FIREWORKS_API_KEY", "fireworks", "https://api.fireworks.ai/inference/v1"},
}

func detectProviderFromEnv() *config.ProviderConfig {
	for _, ep := range envProviders {
		if key := os.Getenv(ep.envVar); key != "" {
			return &config.ProviderConfig{
				Name:    ep.name,
				Type:    ep.typ,
				APIKey:  key,
				BaseURL: ep.baseURL,
			}
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// finalizeSession runs cleanup hooks at session exit: journal summarizer,
// memory export, relationship arc persistence, credit folding, and drift
// detection. Best-effort only — errors are logged but never block exit.
func finalizeSession(a *agent.Agent, providerName, modelName string) {
	homeDir, err := config.ConfigDir()
	if err != nil {
		return
	}

	// Journal summarizer: fire sub-agent to write daily narrative.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		narrateCLISession(ctx, homeDir, a.SessionID(), providerName, modelName)
	}()

	// Memory export: write relationship arc + competence flags.
	go func() {
		exportMemoryIfNeeded(homeDir)
	}()

	// P3: credit assignment — fold session into analyzer.
	go func() {
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
		_ = turns // unused for now

		outcome := credit.OutcomeSuccess
		if errs > 0 && recovs == 0 {
			outcome = credit.OutcomeFailure
		}
		analyzer := credit.NewAnalyzer()
		analyzer.Fold(credit.SessionRecord{
			SessionID: a.SessionID(),
			Outcome:   outcome,
			Actions:   actions,
			Tags:      []string{providerName, modelName},
		})
		// Save to disk.
		store := credit.NewStore(filepath.Join(homeDir, "credit"))
		_ = store.SaveSession(credit.SessionRecord{
			SessionID: a.SessionID(),
			Outcome:   outcome,
			Actions:   actions,
			Tags:      []string{providerName, modelName},
		})
	}()

	// P3: drift detection — compute session metrics and compare to baseline.
	go func() {
		toolCalls, errs, _, turns, _ := a.SessionMetrics()
		sample := make(map[drift.Metric]float64)
		if turns > 0 {
			sample[drift.MetricToolCallsPerTurn] = float64(toolCalls) / float64(turns)
		}
		sample[drift.MetricErrorRate] = float64(errs)
		sample[drift.MetricSessionLength] = float64(turns)

		store := drift.NewStore(filepath.Join(homeDir, "drift", "baseline.json"))
		baseline, _ := store.Load()
		if baseline != nil {
			findings := baseline.Compare(sample, drift.CompareOptions{Threshold: 2.0})
			if len(findings) > 0 {
				log.Printf("drift: %s", drift.FormatFindings(findings))
			}
			baseline.Fold(sample)
			_ = store.Save(baseline)
		}
	}()
}

// runShellCommand executes a shell command directly and prints output.
func runShellCommand(cmd string) {
	out, err := execShellCommand(cmd)
	if err != nil {
		fmt.Printf("%s✗ %s%s\n", colorRed, err, colorReset)
		return
	}
	fmt.Print(out)
}

// execShellCommand runs a shell command and returns its output.
func execShellCommand(cmd string) (string, error) {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	c := exec.Command(sh, "-c", cmd)
	c.Env = os.Environ()
	c.Stdin = os.Stdin
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	out := stdout.String()
	if err != nil {
		return "", fmt.Errorf("%s: %w", stderr.String(), err)
	}
	return out, nil
}

// wrapExtensions adapts *extensions.Manager to agent.ExtensionsManager.
func wrapExtensions(m *extensions.Manager) agent.ExtensionsManager {
	return &extensionsAdapter{mgr: m}
}

type extensionsAdapter struct {
	mgr *extensions.Manager
}

func (e *extensionsAdapter) ListEnabled() []agent.ExtensionMeta {
	if e.mgr == nil {
		return nil
	}
	exts, _ := e.mgr.List()
	out := make([]agent.ExtensionMeta, 0, len(exts))
	for _, ext := range exts {
		if ext.Enabled {
			out = append(out, agent.ExtensionMeta{
				ID:          ext.ID,
				Name:        ext.Name,
				Kind:        string(ext.Kind),
				Description: ext.Description,
			})
		}
	}
	return out
}

// narrateCLISession runs the journal summarizer for the CLI (non-TUI)
// session path. Mirrors writeJournalNarrative but takes raw params
// instead of a TUI App struct.
//
// Uses the cheapest available model from the user's provider for the
// summarization to keep journal costs negligible (§4.19 sub-agent
// pattern — a tool-blocked observer that reads raw flight-recorder
// entries and produces prose).
func narrateCLISession(ctx context.Context, homeDir, sessionID, providerName, modelName string) {
	jdir := filepath.Join(homeDir, "journal")
	rec := journal.NewFlightRecorder(jdir, sessionID)

	pc, _ := resolveProvider()
	if pc == nil {
		return
	}
	apiKey := pc.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(providerEnvVar(pc.Name))
	}
	p, err := providers.NewProvider(providers.FactoryConfig{
		Name:    pc.Name,
		Type:    pc.Type,
		APIKey:  apiKey,
		BaseURL: pc.BaseURL,
	})
	if err != nil {
		return
	}

	// Pick the cheapest model from the provider's list for journal work.
	// Journal summarization is a fire-and-forget task — no tools needed,
	// no vision needed, just a single completion call. The cheapest model
	// that can produce coherent markdown prose wins.
	cheapestModel := pickCheapestModel(p)
	if cheapestModel == "" {
		cheapestModel = modelName // fallback to whatever was passed
	}
	if cheapestModel == "" {
		cheapestModel = cfg.Agent.DefaultModel // config-level default
	}
	if cheapestModel == "" {
		log.Printf("journal narrate: no model configured")
		return
	}

	summ := journal.NewSummarizer(rec, p, cheapestModel)
	path, _, err := summ.NarrateSession(ctx, jdir, sessionID)
	if err != nil {
		log.Printf("journal narrate: %v", err)
		return
	}
	if path != "" {
		log.Printf("journal narrate: wrote %s (model=%s)", path, cheapestModel)
	}
}

// pickCheapestModel returns the ID of the cheapest model (by output cost)
// from the provider's model list. Returns empty string if no models.
func pickCheapestModel(p providers.Provider) string {
	models := p.Models()
	if len(models) == 0 {
		return ""
	}
	best := models[0]
	for i := 1; i < len(models); i++ {
		if models[i].CostOut < best.CostOut {
			best = models[i]
		}
	}
	return best.ID
}

// exportMemoryIfNeeded writes memory-export.md if it doesn't exist
// or is older than 24 hours. Contains a summary of recent sessions
// pulled from the journal entries directory.
func exportMemoryIfNeeded(homeDir string) {
	exportPath := filepath.Join(homeDir, "memory-export.md")
	// Check if recent enough
	if fi, err := os.Stat(exportPath); err == nil {
		if time.Since(fi.ModTime()) < 24*time.Hour {
			return // already fresh
		}
	}

	// Gather what we know from the journal
	entriesDir := filepath.Join(homeDir, "journal", "entries")
	entries, _ := os.ReadDir(entriesDir)

	var b strings.Builder
	b.WriteString("# Overkill Memory Export\n\n")
	b.WriteString("> Auto-generated on session exit.\n")
	b.WriteString("> Last updated: " + time.Now().Format("2006-01-02 15:04:05") + "\n\n")

	b.WriteString("## Recent Journal Entries\n\n")
	if len(entries) == 0 {
		b.WriteString("_(No journal entries yet. Start a session to populate the journal.)_\n\n")
	} else {
		// Show last 10 entries (newest first)
		start := 0
		if len(entries) > 10 {
			start = len(entries) - 10
		}
		for i := len(entries) - 1; i >= start; i-- {
			name := entries[i].Name()
			dateStr := strings.TrimSuffix(name, ".md")
			b.WriteString(fmt.Sprintf("- `%s` — journal entry\n", dateStr))
		}
	}

	// Check for soul.md
	soulPath := filepath.Join(homeDir, "memories", "soul.md")
	if _, err := os.Stat(soulPath); err == nil {
		b.WriteString("\n## Personality\n\n")
		b.WriteString("- Soul file: `memories/soul.md` ✓\n")
		b.WriteString("- Relationship arc: `memories/relationship-arc.json`\n")
	}

	// Check for skills
	skillsDir := filepath.Join(homeDir, "skills")
	if entries, err := os.ReadDir(skillsDir); err == nil && len(entries) > 0 {
		b.WriteString("\n## Active Skills\n\n")
		for _, e := range entries {
			if !e.IsDir() {
				b.WriteString(fmt.Sprintf("- `%s`\n", e.Name()))
			}
		}
	}

	b.WriteString("\n## Data Locations\n\n")
	b.WriteString(fmt.Sprintf("- Journal raw: `%s/journal/raw/`\n", homeDir))
	b.WriteString(fmt.Sprintf("- Journal entries: `%s/journal/entries/`\n", homeDir))
	b.WriteString(fmt.Sprintf("- Memories: `%s/memories/`\n", homeDir))
	b.WriteString(fmt.Sprintf("- Skills: `%s/skills/`\n", homeDir))

	if err := os.WriteFile(exportPath, []byte(b.String()), 0o600); err != nil {
		log.Printf("memory export: write %s: %v", exportPath, err)
	}
}

func buildSystemPrompt(cfg *config.Config) string {
	name := cfg.Agent.Name
	if name == "" {
		name = "Overkill"
	}
	base := fmt.Sprintf(`You are %s, a vibe-coding agent with discipline.
You can run shell commands, read/write files, search code, and interact with git.
Be concise and direct. Never guess URLs. Follow existing code conventions.`, name)

	// Wire cfg.Personality.Language into the system prompt. When set to a
	// non-English locale, instruct the model to respond in that language.
	// TODO: cfg.Personality.Level ("subtle"/"witty"/"full") should also
	// modulate the system prompt's tone/verbosity. Currently only "off"
	// is respected (via the personality provider in runAgent).
	if cfg.Personality.Language != "" && cfg.Personality.Language != "en" {
		base += fmt.Sprintf("\nRespond in %s.", cfg.Personality.Language)
	}
	return base
}

func providerEnvVar(name string) string {
	for _, ep := range envProviders {
		if ep.name == name {
			return ep.envVar
		}
	}
	return strings.ToUpper(name) + "_API_KEY"
}

func init() {
	runCmd.Flags().StringVar(&modelOverride, "model", "", "override default model")
	runCmd.Flags().StringVar(&providerOverride, "provider", "", "override default provider")
	runCmd.Flags().BoolVar(&noPersonality, "no-personality", false, "disable personality engine")
	runCmd.Flags().BoolVar(&noBoot, "no-boot", false, "skip boot animation")
	runCmd.Flags().BoolVar(&latsEnabled, "lats", false, "enable multi-branch LATS tree search (P3)")
	runCmd.Flags().StringVar(&outputFormat, "output", "text", "output format: text (default) or jsonl")
	rootCmd.AddCommand(runCmd)
}

// jsonlProvider wraps a providers.Provider to emit one JSONL record per
// Complete call. Each record captures the turn timestamp, role (always
// "assistant" for Complete), model, token usage, cost estimate, and any
// tool calls dispatched. Cost is computed from the provider's Models()
// pricing table (best-effort — defaults to 0 when unknown).
type jsonlProvider struct {
	inner        providers.Provider
	w            io.Writer
	mu           sync.Mutex
	turn         int
	providerName string
	// modelCosts maps model ID → [costPer1MInput, costPer1MOutput] in USD.
	modelCosts map[string][2]float64
}

func newJSONLProvider(inner providers.Provider, providerName string, w io.Writer) *jsonlProvider {
	jp := &jsonlProvider{
		inner:        inner,
		w:            w,
		providerName: providerName,
		modelCosts:   map[string][2]float64{},
	}
	for _, m := range inner.Models() {
		// Guard against providers that return zero-value models.
		if m.ID == "" {
			continue
		}
		jp.modelCosts[m.ID] = [2]float64{m.CostIn, m.CostOut}
	}
	return jp
}

// EmitUser writes a JSONL record for a user message. turn is the
// per-session turn counter (0 for initial input).
func (p *jsonlProvider) EmitUser(content string, turn int) {
	p.emitRecord(turn, "user", content, "", 0, 0, nil)
}

// Name delegates to the inner provider.
func (p *jsonlProvider) Name() string { return p.inner.Name() }

// Models delegates to the inner provider.
func (p *jsonlProvider) Models() []providers.Model { return p.inner.Models() }

// Complete calls the inner provider and emits a JSONL record.
func (p *jsonlProvider) Complete(ctx context.Context, req providers.Request) (providers.Response, error) {
	p.mu.Lock()
	p.turn++
	turn := p.turn
	p.mu.Unlock()

	resp, err := p.inner.Complete(ctx, req)
	if err != nil {
		return resp, err
	}

	model := resp.Model
	if model == "" {
		model = req.Model
	}
	tokenCount := resp.Usage.InputTokens + resp.Usage.OutputTokens
	cost := p.estimateCost(model, resp.Usage.InputTokens, resp.Usage.OutputTokens)

	p.emitRecord(turn, "assistant", resp.Content, model, tokenCount, cost, resp.ToolCalls)
	return resp, nil
}

// Stream delegates to the inner provider (not used by the run command but
// required by the interface).
func (p *jsonlProvider) Stream(ctx context.Context, req providers.Request) (<-chan providers.Chunk, error) {
	return p.inner.Stream(ctx, req)
}

// estimateCost computes cost in USD from cached model pricing. Returns 0
// when the model is unknown or pricing data is unavailable.
func (p *jsonlProvider) estimateCost(model string, inputTokens, outputTokens int) float64 {
	p.mu.Lock()
	costs, ok := p.modelCosts[model]
	p.mu.Unlock()
	if !ok {
		return 0
	}
	costPer1MIn, costPer1MOut := costs[0], costs[1]
	return (float64(inputTokens)*costPer1MIn + float64(outputTokens)*costPer1MOut) / 1_000_000
}

// jsonlRecord is the flat JSON object emitted per turn.
type jsonlRecord struct {
	Timestamp  string               `json:"timestamp"`
	Turn       int                  `json:"turn"`
	Role       string               `json:"role"`
	Content    string               `json:"content"`
	Model      string               `json:"model"`
	Provider   string               `json:"provider"`
	TokensUsed int                  `json:"tokens_used"`
	CostUSD    float64              `json:"cost_usd"`
	ToolCalls  []providers.ToolCall `json:"tool_calls,omitempty"`
}

func (p *jsonlProvider) emitRecord(turn int, role, content, model string, tokensUsed int, costUSD float64, toolCalls []providers.ToolCall) {
	rec := jsonlRecord{
		Timestamp:  time.Now().Format(time.RFC3339Nano),
		Turn:       turn,
		Role:       role,
		Content:    content,
		Model:      model,
		Provider:   p.providerName,
		TokensUsed: tokensUsed,
		CostUSD:    costUSD,
	}
	if len(toolCalls) > 0 {
		rec.ToolCalls = toolCalls
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	fmt.Fprintln(p.w, string(b))
}

// memoryOrchRetriever adapts the memory orchestrator to agent.MemoryRetriever.
type memoryOrchRetriever struct {
	orch *memory.Orchestrator
}

func (r *memoryOrchRetriever) Search(ctx context.Context, query string, k int) ([]agent.MemoryHit, error) {
	if r == nil || r.orch == nil {
		return nil, nil
	}
	res, err := r.orch.SemanticRecall(ctx, query, k)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	hits := make([]agent.MemoryHit, 0, len(res.Memories))
	for _, m := range res.Memories {
		hits = append(hits, agent.MemoryHit{
			ID:    m.ID,
			Text:  m.Content,
			Score: m.Relevance,
		})
	}
	return hits, nil
}

// bookmarkStoreAdapter adapts gateway.BookmarkStore to agent.SessionBookmarkStore.
type bookmarkStoreAdapter struct {
	store *gateway.BookmarkStore
}

func (a *bookmarkStoreAdapter) Save(sessionID, label string) error {
	_, err := a.store.Save(sessionID, label)
	return err
}

// sessIDProvider adapts a func() string to the tools.SessionIDProvider interface.
type sessIDProvider func() string

func (s sessIDProvider) SessionID() string { return s() }
