package main

import (
	"context"
	encjson "encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"path/filepath"

	"github.com/Sahaj-Tech-ltd/ethos/internal/acp"
	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
	syncpkg "github.com/Sahaj-Tech-ltd/ethos/internal/sync"
	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/hooks"
	"github.com/Sahaj-Tech-ltd/ethos/internal/journal"
	"github.com/Sahaj-Tech-ltd/ethos/internal/lsp"
	"github.com/Sahaj-Tech-ltd/ethos/internal/mcp"
	pluginpkg "github.com/Sahaj-Tech-ltd/ethos/internal/plugin"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/security"
	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
	"github.com/Sahaj-Tech-ltd/ethos/internal/skills"
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
	if app.Tags != nil {
		toolReg.Register(tools.NewTagAddTool(app.Tags))
		toolReg.Register(tools.NewTagRemoveTool(app.Tags))
		toolReg.Register(tools.NewTagListTool(app.Tags))
	}

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

	app.Agent = a

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

// jsonMarshal is a tiny helper that swallows marshal errors and returns empty
// bytes — keeps the event handler one-liner readable.
func jsonMarshal(v any) ([]byte, error) {
	b, err := encjson.Marshal(v)
	if err != nil {
		return []byte("{}"), err
	}
	return b, nil
}
