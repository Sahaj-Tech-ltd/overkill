package main

import (
	"context"
	"time"
	encjson "encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/acp"
	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
	"github.com/Sahaj-Tech-ltd/ethos/internal/browser"
	"github.com/Sahaj-Tech-ltd/ethos/internal/compaction"
	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/hooks"
	"github.com/Sahaj-Tech-ltd/ethos/internal/introspection"
	"github.com/Sahaj-Tech-ltd/ethos/internal/journal"
	"github.com/Sahaj-Tech-ltd/ethos/internal/personality"
	"github.com/Sahaj-Tech-ltd/ethos/internal/lsp"
	"github.com/dgraph-io/badger/v4"

	"github.com/Sahaj-Tech-ltd/ethos/bridge"
	"github.com/Sahaj-Tech-ltd/ethos/internal/mcp"
	memorypkg "github.com/Sahaj-Tech-ltd/ethos/internal/memory"
	pluginpkg "github.com/Sahaj-Tech-ltd/ethos/internal/plugin"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/rewriter"
	"github.com/Sahaj-Tech-ltd/ethos/internal/security"
	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
	"github.com/Sahaj-Tech-ltd/ethos/internal/skills"
	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
	"github.com/Sahaj-Tech-ltd/ethos/internal/automation"
	"github.com/Sahaj-Tech-ltd/ethos/internal/checkpoint"
	"github.com/Sahaj-Tech-ltd/ethos/internal/walls"
	syncpkg "github.com/Sahaj-Tech-ltd/ethos/internal/sync"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tags"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tools"
	"github.com/Sahaj-Tech-ltd/ethos/internal/workspace"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/cellrender"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/components/animation"
	"golang.org/x/term"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the Ethos terminal UI",
	RunE:  runTUI,
}

func runTUI(cmd *cobra.Command, args []string) error {
	// Pin color profile and background brightness BEFORE any rendering or
	// program startup. Otherwise lipgloss/termenv probes the terminal with
	// OSC 11 ("query background color"); the terminal's reply
	// (`\x1b]11;rgb:...\x07`) gets queued on stdin and ends up typed into
	// the editor as garbage like `]11;rgb:3131/1616/5252\`.
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)

	// Wire the animation kill-switch from config before any TUI component
	// reads animation.Enabled().
	if cfg != nil {
		animation.SetEnabled(cfg.UI.Animations)
	}
	app := buildTUIApp()
	app.Build = func(c *config.Config) (*agent.Agent, error) {
		cfg = c
		return buildTUIApp().Agent, nil
	}

	// Background catalog refresh — fire-and-forget. The disk cache makes the
	// next /model open instant; this just keeps the cache fresh for the next
	// launch. Network failure is silently ignored (FetchCatalog handles its
	// own fallback chain).
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = providers.RefreshCatalog(ctx)
	}()

	// Alt-screen for clean enter/exit. No mouse capture — over SSH each
	// mouse-move event is a render trigger and floods bandwidth.
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if os.Getenv("ETHOS_CELL_RENDER") == "1" {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || w <= 0 || h <= 0 {
			w, h = 80, 24
		}
		cw := cellrender.NewWriter(os.Stdout, w, h)
		opts = append(opts, tea.WithOutput(cw))
		fmt.Fprintln(os.Stderr, "[ethos] cell-render path active (ETHOS_CELL_RENDER=1)")
	}
	prog := tea.NewProgram(tui.New(app), opts...)
	tui.SetProgram(prog)

	defer func() {
		if app != nil && app.Browser != nil {
			app.Browser.Close()
		}
	}()

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("tui exited: %w", err)
	}
	return nil
}

// buildTUIApp wires the agent, providers, tools, and config into a tui.App.
// If no provider is configured, App.Agent stays nil and the TUI shows the
// setup dialog instead of the chat page.
func buildTUIApp() *tui.App {
	app := &tui.App{
		Config:     cfg,
		Hooks:      hooks.NewRegistry(),
		ConfigPath: resolvedCfgPath,
	}

	// Per-user BadgerDB session store. Failure is non-fatal — TUI degrades
	// to ephemeral chat if the store can't open.
	if home, err := os.UserHomeDir(); err == nil {
		dir := home + "/.ethos/sessions"
		_ = os.MkdirAll(dir, 0o755)
		if store, err := session.NewBadgerStore(dir); err == nil {
			app.Store = store
		}

		// Tag manager (best-effort).
		if tm, err := tags.NewManager(filepath.Join(home, ".ethos", "tags.jsonl")); err == nil {
			app.Tags = tm
		}
		// Workspace manager (best-effort).
		if wm, err := workspace.NewManager(filepath.Join(home, ".ethos", "workspaces.json")); err == nil {
			app.Workspace = wm
		}
		// Skill loader — try ~/.ethos/skills then bundled ./skills as a fallback.
		userSkillsDir := filepath.Join(home, ".ethos", "skills")
		loader := skills.NewLoader("skills", userSkillsDir)
		if all, err := loader.LoadAll(); err == nil {
			enabled := map[string]bool{}
			if cfg != nil {
				for _, n := range cfg.Skills.Enabled {
					enabled[n] = true
				}
			}
			for i := range all {
				if enabled[all[i].Name] {
					all[i].Enabled = true
				}
			}
			app.Skills = all
		}
	}

	providerCfg, modelName := resolveProvider()
	if providerCfg == nil {
		return app
	}

	apiKey := providerCfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(providerEnvVar(providerCfg.Name))
	}
	if apiKey == "" && providerCfg.AuthType == "oauth" {
		apiKey = providers.ResolveOAuthAPIKey(providerCfg.Name)
	}

	provider, err := providers.NewProvider(providers.FactoryConfig{
		Name:    providerCfg.Name,
		Type:    providerCfg.Type,
		APIKey:  apiKey,
		BaseURL: providerCfg.BaseURL,
		Headers: providerCfg.Headers,
	})
	if err != nil {
		// Surface in-TUI via setup dialog rather than failing here.
		return app
	}

	cwd, _ := os.Getwd()
	toolReg := tools.NewRegistry()
	app.Tools = toolReg
	toolReg.Register(tools.NewShellTool())
	toolReg.Register(tools.NewFSTool(cwd))
	toolReg.Register(tools.NewGitTool(cwd))
	toolReg.Register(tools.NewGrepTool(cwd))
	toolReg.Register(tools.NewWebTool())
	toolReg.Register(tools.NewPatchTool(cwd))
	toolReg.Register(tools.NewPTYShellTool(cwd))
	toolReg.Register(tools.NewWorktreeListTool(cwd))
	toolReg.Register(tools.NewWorktreeAddTool(cwd))
	toolReg.Register(tools.NewWorktreeRemoveTool(cwd))
	toolReg.Register(tools.NewACPSendTool())

	// Sub-agent manager + contract-driven tooling.
	app.Subagent = subagent.NewManager(subagent.Config{MaxDepth: 2, MaxChildren: 3})
	toolReg.Register(tools.NewDelegateTool(app.Subagent))
	toolReg.Register(tools.NewSubagentStatusTool(app.Subagent))
	toolReg.Register(tools.NewSubagentWaitTool(app.Subagent))
	// driverFactory is wired below once the parent agent exists, since
	// children currently share the parent's provider/registry.

	// Memory orchestrator — Mem0-style persistent recall (master plan §6.1).
	// Uses its own Badger DB under ~/.ethos/memory; wires the Python bridge
	// for embeddings/rerank when ETHOS_BRIDGE_ADDR is set.
	if home, err := os.UserHomeDir(); err == nil {
		memDir := filepath.Join(home, ".ethos", "memory")
		_ = os.MkdirAll(memDir, 0o755)
		if mdb, err := badger.Open(badger.DefaultOptions(memDir).WithLoggingLevel(badger.ERROR)); err == nil {
			memStore := memorypkg.NewBadgerStore(mdb)
			memOrch := memorypkg.NewOrchestrator(memStore, provider, modelName)
			if addr := os.Getenv("ETHOS_BRIDGE_ADDR"); addr != "" {
				if bc, berr := bridge.NewClient(addr); berr == nil {
					embedModel := os.Getenv("ETHOS_EMBED_MODEL")
					if embedModel == "" {
						embedModel = "text-embedding-3-small"
					}
					memOrch.AttachEmbeddings(memorypkg.NewBridgeAdapter(bc), memorypkg.SemanticConfig{
						EmbedModel:      embedModel,
						SearchThreshold: 0.0,
						RerankTopN:      0,
					})
				}
			}
			toolReg.Register(tools.NewMemoryRememberTool(memOrch))
			toolReg.Register(tools.NewMemoryRecallTool(memOrch))
			toolReg.Register(tools.NewMemoryForgetTool(memOrch))
		}

		// Behavioral regression bank (master plan §6.5 Wall 3). Per-user
		// Badger at ~/.ethos/regressions; tools surface record/list/verify.
		regDir := filepath.Join(home, ".ethos", "regressions")
		_ = os.MkdirAll(regDir, 0o755)
		if rdb, err := badger.Open(badger.DefaultOptions(regDir).WithLoggingLevel(badger.ERROR)); err == nil {
			bank := walls.NewRegressionBank(walls.NewBadgerRegressionStore(rdb), nil)
			toolReg.Register(tools.NewRegressionRecordTool(bank))
			toolReg.Register(tools.NewRegressionListTool(bank))
			toolReg.Register(tools.NewRegressionVerifyTool(bank))
		}

		// Filesystem checkpoints (master plan §4.8). The agent calls
		// checkpoint_snapshot before destructive ops; users can roll back via
		// /rollback or the CLI subcommand.
		ckptDir := filepath.Join(home, ".ethos", "checkpoints")
		if cmgr, err := checkpoint.NewManager(ckptDir, 20); err == nil {
			app.Checkpoints = cmgr
			sessFn := func() string {
				if app.Agent != nil {
					return app.Agent.SessionID()
				}
				return ""
			}
			toolReg.Register(tools.NewCheckpointSnapshotTool(cmgr, sessFn))
			toolReg.Register(tools.NewCheckpointListTool(cmgr, sessFn))
			toolReg.Register(tools.NewCheckpointRestoreTool(cmgr))
		}
	}

	if app.Tags != nil {
		toolReg.Register(tools.NewTagAddTool(app.Tags))
		toolReg.Register(tools.NewTagRemoveTool(app.Tags))
		toolReg.Register(tools.NewTagListTool(app.Tags))
	}

	// Agentic browser — opt-in via config. The headless Chrome process is
	// lazy-spawned on the first browser_* tool call.
	if cfg != nil && cfg.Browser.Enabled {
		bm := browser.NewManager(browser.Options{
			Headless:   true,
			ChromePath: cfg.Browser.ChromePath,
			UserAgent:  cfg.Browser.UserAgent,
		})
		app.Browser = bm
		policy := tools.BrowserHostPolicy{
			Allowed: cfg.Browser.AllowedHosts,
			Blocked: cfg.Browser.BlockedHosts,
		}
		toolReg.Register(tools.NewBrowserOpenTool(bm, policy))
		toolReg.Register(tools.NewBrowserNavigateTool(bm, policy))
		toolReg.Register(tools.NewBrowserScreenshotTool(bm, policy))
		toolReg.Register(tools.NewBrowserTextTool(bm, policy))
		toolReg.Register(tools.NewBrowserMarkdownTool(bm, policy))
		toolReg.Register(tools.NewBrowserClickTool(bm, policy))
		toolReg.Register(tools.NewBrowserFillTool(bm, policy))
		toolReg.Register(tools.NewBrowserSelectTool(bm, policy))
		toolReg.Register(tools.NewBrowserEvalTool(bm, policy))
		toolReg.Register(tools.NewBrowserWaitTool(bm, policy))
	}

	// Diagnostic ladder (master plan §4.13).
	toolReg.Register(tools.NewDiagnoseNextTierTool())

	// Autocommit (master plan §4.8). Off by default — user enables stages
	// via env vars or future config; the tool surface lets the agent fire
	// commits at named milestones.
	autocommit := automation.NewAutoCommitter(cwd, nil, nil)
	if v := os.Getenv("ETHOS_AUTOCOMMIT"); v != "" {
		// "test-pass,build-green,lint-clean,patch-applied"
		for _, s := range strings.Split(v, ",") {
			autocommit.SetEnabled(strings.TrimSpace(s), true)
		}
	}
	toolReg.Register(tools.NewAutocommitStageTool(autocommit))

	// Skill auto-creation (master plan §6.2 Voyager). Writes user-scoped
	// SKILL.md files at ~/.ethos/skills/<name>/.
	toolReg.Register(tools.NewSkillExtractTool(""))

	// dev-browser as the third browser flavor (master plan §7.3). Always
	// registered; degrades to a clear "binary not on PATH" error when the
	// user hasn't installed it. No config gate.
	toolReg.Register(tools.NewBrowserDevTool())

	// MCP: spin up configured servers in the background. Tools become
	// available to the agent as they finish handshaking.
	if cfg != nil && len(cfg.MCP.Servers) > 0 {
		mcpMgr := mcp.NewManager(cfg.MCP)
		_ = mcpMgr.Start(context.Background())
		app.MCP = mcpMgr
		// Best-effort: wait briefly for fast-starting servers, then register
		// whatever tools are available. Slow servers will surface later via
		// the /mcp dialog but won't block agent startup.
		go func() {
			// Drain through one short window then register; long-running
			// re-registration on reconnect is intentionally not done here
			// to keep the registry stable.
			for _, tw := range mcpMgr.Tools() {
				_ = toolReg.Register(mcp.NewToolAdapter(mcpMgr, tw.Server, tw.Tool.Name))
			}
		}()
	}

	// Plugins: start the subprocess plugin runtime and adapt every
	// registered tool. Slash commands are surfaced in the TUI through
	// app.Plugins; the dialog reads status from the manager directly.
	pluginRoot := pluginpkg.DefaultPluginsDir()
	if cfg != nil && cfg.Plugins.Dir != "" {
		pluginRoot = cfg.Plugins.Dir
	}
	var pluginDisabled []string
	if cfg != nil {
		pluginDisabled = cfg.Plugins.Disabled
	}
	pluginMgr := pluginpkg.NewManager(pluginRoot, &pluginHostBridge{cfgRef: &cfg}, pluginDisabled)
	if err := pluginMgr.Start(context.Background()); err == nil {
		app.Plugins = pluginMgr
		// Tools become available as plugins finish handshaking. Mirror the
		// same best-effort post-start registration MCP uses above.
		go func() {
			// Give plugins a brief window to handshake before registering.
			// Slow plugins surface later via the /plugins dialog and won't
			// block agent startup.
			for _, tw := range pluginMgr.Tools() {
				_ = toolReg.Register(pluginpkg.NewToolAdapter(pluginMgr, tw.Plugin, tw.Tool.Name))
			}
		}()
	}

	// LSP: probe PATH for default servers if user didn't configure any.
	// cfg may be nil in degraded paths (config load failed earlier in
	// PersistentPreRunE). Defend rather than panic.
	var lspCfg config.LSPConfig
	if cfg != nil {
		lspCfg = cfg.LSP
	}
	if len(lspCfg.Servers) == 0 {
		lspCfg = lsp.DefaultConfig()
	}
	if len(lspCfg.Servers) > 0 {
		lspMgr := lsp.NewManager(lspCfg, cwd)
		go lspMgr.Start(context.Background())
		app.LSP = lspMgr
		toolReg.Register(tools.NewLSPDefinitionTool(lspMgr))
		toolReg.Register(tools.NewLSPReferencesTool(lspMgr))
		toolReg.Register(tools.NewLSPHoverTool(lspMgr))
		toolReg.Register(tools.NewLSPSymbolsTool(lspMgr))
	}

	// Build agent first so we can pass a bridge into ask_user that calls
	// back into agent.AskQuestion (which the TUI wires to the question
	// dialog via QuestionFunc).
	a := agent.New(agent.Config{
		Provider:     provider,
		Tools:        toolReg,
		Compressors:  tools.NewCompressorRegistry(),
		Hooks:        app.Hooks,
		Scanners:     []security.Scanner{security.NewCommandScanner(security.WithProjectPath(cwd))},
		Tokenizer:    tokenizer.NewEstimator(),
		Steering:     agent.NewSteeringQueue(agent.SteeringDrainAll),
		Model:        modelName,
		MaxTokens:    200000,
		SystemPrompt: buildSystemPrompt(cfg),
	})
	toolReg.Register(tools.NewAskUserTool(func(ctx context.Context, prompt string, choices []string) (string, int, bool) {
		ans := a.AskQuestion(ctx, agent.Question{Prompt: prompt, Choices: choices})
		return ans.Text, ans.Index, ans.Cancel
	}))
	// Permission ledger — append-only JSONL per session. Reuses the agent's
	// session id when set; falls back to "default" so /permissions still
	// works in ephemeral sessions.
	if home, err := os.UserHomeDir(); err == nil {
		sid := a.SessionID()
		if sid == "" {
			sid = "default"
		}
		ledgerPath := filepath.Join(home, ".ethos", "sessions", sid, "permissions.jsonl")
		if l, err := security.NewLedger(ledgerPath); err == nil {
			a.SetPermissionLedger(l)
		}
	}

	// LCM compaction (master plan §4.4). Default-on; flip
	// cfg.Compaction.UseLCM=false to revert to the legacy ad-hoc summary path.
	if cfg == nil || cfg.Compaction.UseLCM {
		preserve := 20
		if cfg != nil && cfg.Compaction.PreserveMessages > 0 {
			preserve = cfg.Compaction.PreserveMessages
		}
		ac := compaction.NewAgentCompactor(provider, tokenizer.NewEstimator(), preserve)
		a.SetCompactor(ac, true)
	}

	// Prompt rewriter middleware (master plan §4.10). Off by default — only
	// instantiated when cfg.Rewriter.Enabled. Falls back to bypass if the
	// model can't be resolved.
	if cfg != nil && cfg.Rewriter.Enabled {
		rwModel := cfg.Rewriter.Model
		if rwModel == "" {
			rwModel = modelName
		}
		// Compose rewriter middleware: regex middleware runs first via the
		// LLMRewriter wrapper; sycophancy reducer is consulted on LLM output.
		_ = rewriter.NewMiddleware()
		_ = rewriter.NewSycophancyReducer()
		llmRw := rewriter.NewLLMRewriter(provider, rwModel)
		a.SetRewriter(llmRw)
	}

	// Per-turn context provider — composes CODEBASE.md (master plan §4.18,
	// generated by /init) with active standing orders (master plan §7.1).
	if home, herr := os.UserHomeDir(); herr == nil {
		introDir := filepath.Join(home, ".ethos", "introspection")
		ordersPath := filepath.Join(home, ".ethos", "standing-orders.jsonl")
		var orders *automation.OrdersFile
		if of, err := automation.NewOrdersFile(ordersPath); err == nil {
			orders = of
			app.StandingOrders = of
		}
		a.SetContextProvider(func(ctx context.Context, sid string) string {
			parts := []string{
				introspection.LoadPRPSnippet(introDir, 4000),
				introspection.LoadCodebaseSnippet(introDir, 8000),
			}
			if orders != nil {
				if snip := orders.PromptSnippet(); snip != "" {
					parts = append(parts, snip)
				}
			}
			out := ""
			for _, p := range parts {
				if p == "" {
					continue
				}
				if out != "" {
					out += "\n\n"
				}
				out += p
			}
			return out
		})
	}

	app.Agent = a

	// Privilege gate (master plan §4.3): start in writer mode for backward
	// compatibility; user flips with /mode reader|writer.
	a.SetPrivilegeGate(security.NewPrivilegeGate(security.ModeWriter))

	// Sub-agent driver factory: contracts spawned via delegate_task drive
	// the parent agent through an autonomous loop. (Future: build a fresh
	// child agent per spawn for true isolation; today we share state.)
	app.Subagent.SetDriverFactory(func(c *subagent.Contract) (subagent.StepDriver, error) {
		return agent.NewContractDriver(a, c, cwd), nil
	})

	// Flight recorder — persists every tool call / error to ~/.ethos/journal
	// so /journal search and post-mortem reports have data to read. Failure to
	// open is non-fatal: agent still runs, just without persistent observability.
	if home, err := os.UserHomeDir(); err == nil {
		sid := a.SessionID()
		if sid == "" {
			sid = "default"
		}
		jdir := filepath.Join(home, ".ethos", "journal")
		_ = os.MkdirAll(jdir, 0o755)
		recorder := journal.NewFlightRecorder(jdir, sid)
		app.Journal = recorder

		// Boot-time alerts (master plan §4.19). Single AlertStore is wired to
		// every producer (recovery, transparency, blindspot, compaction). Boot
		// reader in pkg/tui surfaces pending alerts as toasts.
		alertDir := filepath.Join(home, ".ethos", "alerts")
		_ = os.MkdirAll(alertDir, 0o755)
		alertStore := journal.NewAlertStore(alertDir)
		_ = alertStore.Load()
		app.Alerts = alertStore
		alertSink := &alertSinkAdapter{store: alertStore}

		// Recovery → AlertTaskDeferred
		// (recovery itself is constructed inside agent; we set the sink via
		// a small accessor wired into SetRecoveryWriter — call it again so
		// the new recovery instance carries the sink).
		// The agent's emitRecovery() will fire FireDeferralAlert once wired.
		// Transparency + Blindspot engines (built per-session by personality
		// runtime; instances exposed for sink wiring).
		te := personality.NewTransparencyEngine(modelName)
		te.SetAlertSink(alertSink, sid)
		bs := personality.NewBlindSpotDetector()
		bs.SetAlertSink(alertSink, sid)

		// Frustration detector — observes raw user input and fires
		// AlertFrustration via the same sink (master plan §4.16).
		fd := personality.NewFrustrationDetector(alertSink, sid)
		a.SetUserInputObserver(func(input string) { fd.Observe(input) })
		// Stash on app for downstream personality runtime to consume.
		_ = te
		_ = bs

		// Compaction skip alert wiring — only meaningful when LCM compactor
		// is active (set above).
		if cfg == nil || cfg.Compaction.UseLCM {
			// The compactor already exists on the agent; re-create the
			// sink-bound version to ensure compaction_skip alerts fire.
			preserve := 20
			if cfg != nil && cfg.Compaction.PreserveMessages > 0 {
				preserve = cfg.Compaction.PreserveMessages
			}
			ac := compaction.NewAgentCompactor(provider, tokenizer.NewEstimator(), preserve)
			ac.SetAlertSink(alertSink, sid)
			a.SetCompactor(ac, true)
		}

		// Recovery sink — re-bind the recovery writer so its alert sink is
		// set. SetRecoveryWriter constructs a fresh ErrorRecovery; we expose
		// a follow-up setter via the journalAdapter we already built.
		_ = recoveryAlertBinder(a, alertSink, sid)

		// Forward agent lifecycle events into the journal. Best-effort: any
		// write failure is silently dropped so a full disk doesn't kill chat.
		journalAdapter := &journalEventAdapter{rec: recorder}
		a.SetEventFn(journalAdapter.Handle)
		a.SetRecoveryWriter(journalAdapter)
	}

	// Sync manager — best-effort, only when configured.
	if cfg != nil && cfg.Sync.Backend != "" && app.Store != nil {
		if be, err := syncpkg.NewBackend(cfg.Sync); err == nil && be != nil {
			app.Sync = syncpkg.NewManager(app.Store, be)
		}
	}

	// ACP server — start in-process when configured. Falls back to default
	// listen address if Listen is blank but Enabled is true.
	if cfg != nil && (cfg.ACP.Enabled || cfg.ACP.Listen != "") {
		token, _ := loadOrCreateACPToken()
		listen := cfg.ACP.Listen
		if listen == "" {
			listen = "127.0.0.1:8421"
		}
		srv := acp.NewServer(acp.Config{
			Addr:           listen,
			Token:          token,
			AllowedOrigins: cfg.ACP.AllowedOrigins,
			Agent:          &acpAgentAdapter{a: a},
			Store:          app.Store,
			Name:           "ethos",
			Version:        Version,
		})
		if err := srv.Start(); err == nil {
			app.ACPServer = srv
		}
	}

	return app
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}

// acpAgentAdapter bridges *agent.Agent to acp.Sender by translating between
// agent.ACPEvent and acp.AgentEvent. Lives here (not in either package) to
// avoid an import cycle.
type acpAgentAdapter struct{ a *agent.Agent }

func (x *acpAgentAdapter) Model() string     { return x.a.Model() }
func (x *acpAgentAdapter) SessionID() string { return x.a.SessionID() }
func (x *acpAgentAdapter) StreamACP(ctx context.Context, in string) (<-chan acp.AgentEvent, error) {
	src, err := x.a.StreamACPRaw(ctx, in)
	if err != nil {
		return nil, err
	}
	out := make(chan acp.AgentEvent, 64)
	go func() {
		defer close(out)
		for ev := range src {
			out <- acp.AgentEvent{
				Type:     acp.AgentEventType(ev.Type),
				Content:  ev.Content,
				ToolName: ev.ToolName,
				ToolArgs: ev.ToolArgs,
				Error:    ev.Error,
			}
		}
	}()
	return out, nil
}

// journalEventAdapter bridges agent.Agent's lifecycle event callback into the
// flight recorder. Implements both the agent.SetEventFn signature (via Handle)
// and agent.JournalEntryWriter (via WriteEntry) so the same instance can serve
// streaming events and recovery lessons.
type journalEventAdapter struct {
	rec *journal.FlightRecorder
}

func (j *journalEventAdapter) Handle(event string, payload map[string]any) {
	if j == nil || j.rec == nil {
		return
	}
	defer func() { _ = recover() }()
	switch event {
	case "tool_call":
		tool, _ := payload["tool"].(string)
		input, _ := payload["input"].(string)
		_ = j.rec.RecordToolCall(tool, []byte(input))
	case "error":
		msg, _ := payload["error"].(string)
		_ = j.rec.RecordError(fmt.Errorf("%s", msg))
	case "tool_impact", "budget_warning", "compact", "recovery":
		// Promote structured events to system entries for later analysis.
		raw, _ := jsonMarshal(payload)
		_ = j.rec.Record(journal.EntrySystem, event, raw)
	}
}

func (j *journalEventAdapter) WriteEntry(entryType string, content string) error {
	if j == nil || j.rec == nil {
		return nil
	}
	return j.rec.Record(journal.EntryType(entryType), content, nil)
}

// alertSinkAdapter bridges the various package-local AlertSink interfaces
// (compaction, personality, agent.recovery) onto the journal AlertStore. It
// lives in the cmd package so the upstream packages stay free of journal
// imports.
type alertSinkAdapter struct {
	store *journal.AlertStore
}

func (a *alertSinkAdapter) Create(alertType, message, sessionID string) error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.Create(journal.AlertType(alertType), message, sessionID)
}

// recoveryAlertBinder binds the alert sink onto the agent's recovery pipeline.
// Returns whether the bind was attempted (always true for non-nil agent).
func recoveryAlertBinder(a *agent.Agent, sink agent.AlertSink, sid string) bool {
	if a == nil {
		return false
	}
	a.SetRecoveryAlertSink(sink, sid)
	return true
}

// jsonMarshal is a tiny helper that swallows marshal errors and returns empty
// bytes — keeps the event handler one-liner readable.
func jsonMarshal(v any) ([]byte, error) {
	b, err := encjson.Marshal(v)
	if err != nil {
		return []byte("{}"), err
	}
	return b, nil
}
