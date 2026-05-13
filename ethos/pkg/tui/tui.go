package tui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/introspection"
	mcppkg "github.com/Sahaj-Tech-ltd/overkill/internal/mcp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plugin"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/share"
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	"github.com/Sahaj-Tech-ltd/overkill/internal/workspace"
	wt "github.com/Sahaj-Tech-ltd/overkill/internal/worktree"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/chat"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/dialog"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/logo"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/onboarding"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/sidebar"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/status"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/viewer"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/layout"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/page"
	tuitypes "github.com/Sahaj-Tech-ltd/overkill/pkg/tui/types"
)

const defaultSidebarWidth = 30
const sidebarRefreshInterval = 5 * time.Second
const subagentTickInterval = 1 * time.Second

// dialogKind identifies which overlay (if any) is currently visible.
type dialogKind int

const (
	dialogNone dialogKind = iota
	dialogCommands
	dialogModels
	dialogSessions
	dialogTheme
	dialogConfig
	dialogHelp
	dialogStatus
	dialogPermission
	dialogTimeline
	dialogRename
	dialogDeleteConfirm
	dialogStash
	dialogFileMention
	dialogQuestion
	dialogDiff
	dialogMCP
	dialogWorktree
	dialogPermissionsLedger
	dialogTag
	dialogVariant
	dialogWorkspace
	dialogSubagentFull
	dialogSkill
	dialogProviderSetup
	dialogOverrideConfirm
	dialogPlugins
	dialogBrowser
)

type appModel struct {
	width       int
	height      int
	showSidebar bool

	boot      BootModel
	person    *personality.Personality
	chatPage  page.ChatPage
	statusBar status.StatusBarModel
	sidebar   sidebar.SidebarModel
	toast     status.ToastModel

	cmdDialog        dialog.CommandDialog
	modelDialog      dialog.ModelDialog
	sessionDialog    dialog.SessionDialog
	themeDialog      dialog.ThemeDialog
	setupDialog      dialog.SetupDialog
	helpDialog       dialog.HelpDialog
	statusDialog     dialog.StatusDialog
	permissionDialog dialog.PermissionDialog
	timelineDialog   dialog.TimelineDialog
	renameDialog     dialog.SessionRenameDialog
	stashDialog      dialog.StashDialog
	fileMentionDlg   dialog.FileMentionDialog
	questionDialog   dialog.QuestionDialog
	diffDialog       dialog.DiffDialog
	mcpDialog        dialog.MCPDialog
	worktreeDialog   dialog.WorktreeDialog
	permLedgerDlg    dialog.PermissionsLedgerDialog
	tagDialog        dialog.TagDialog
	variantDialog    dialog.VariantDialog
	workspaceDialog  dialog.WorkspaceDialog
	subagentFullDlg  dialog.SubagentFullDialog
	skillDialog      dialog.SkillDialog
	provSetupDialog  dialog.ProviderSetupDialog
	overrideDialog   dialog.OverrideConfirmDialog
	pluginsDialog    dialog.PluginsDialog
	browserDialog    dialog.BrowserDialog
	splitView        *viewer.FileView
	splitOpen        bool
	splitFocused     bool
	stashStore       *session.StashStore
	deleteSession    *session.Session
	openDialog       dialogKind

	// subagentFooterCursor is the index of the focused subagent footer entry.
	// -1 means no focus (default — keys flow to the editor as normal). Set
	// when the user presses ↑ from an empty editor with subagents running.
	subagentFooterCursor int

	filesPanel   sidebar.FilesPanel
	costPanel    sidebar.CostPanel
	sessionPanel sidebar.SessionPanel

	currentSession *session.Session

	// connection status: 0 unknown, 1 ok, 2 retrying, 3 down
	connState  int
	failStreak int

	// firstRunSetup is true when no providers are configured at startup.
	firstRunSetup bool

	// quitArmed tracks the ctrl+c double-press window. When non-zero, a second
	// ctrl+c within 2 seconds exits cleanly. Otherwise we toast and arm.
	quitArmedAt time.Time

	// escArmedAt tracks the esc double-press window. When non-zero and no
	// dialog is open, esc arms the exit; a second esc within 2s quits.
	escArmedAt time.Time

	app *App

	// onboarding wizard. nil when the marker file already exists; non-nil
	// during the first-run flow. Routes all keys + renders in lieu of chat
	// while active.
	onboarding *onboarding.Model

	// Render rate limiter — cap View() at ~60fps to keep SSH bandwidth and
	// re-paint cost bounded. Cleared/refreshed on every real render.
	lastRenderAt  time.Time
	lastRenderOut string
}

func New(app *App) tea.Model {
	m := &appModel{
		boot:                 BootModel{visible: true},
		showSidebar:          true,
		statusBar:            status.NewStatusBar(),
		sidebar:              sidebar.NewSidebar(),
		toast:                status.NewToastModel(),
		setupDialog:          dialog.NewSetupDialog(),
		cmdDialog:            dialog.NewCommandDialog(),
		modelDialog:          dialog.NewModelDialog(),
		sessionDialog:        dialog.NewSessionDialog(),
		themeDialog:          dialog.NewThemeDialog(),
		helpDialog:           dialog.NewHelpDialog(),
		statusDialog:         dialog.NewStatusDialog(),
		permissionDialog:     dialog.NewPermissionDialog(),
		timelineDialog:       dialog.NewTimelineDialog(),
		renameDialog:         dialog.NewSessionRenameDialog(),
		stashDialog:          dialog.NewStashDialog(),
		fileMentionDlg:       dialog.NewFileMentionDialog(),
		questionDialog:       dialog.NewQuestionDialog(),
		diffDialog:           dialog.NewDiffDialog(),
		mcpDialog:            dialog.NewMCPDialog(),
		worktreeDialog:       dialog.NewWorktreeDialog(),
		permLedgerDlg:        dialog.NewPermissionsLedgerDialog(),
		tagDialog:            dialog.NewTagDialog(),
		variantDialog:        dialog.NewVariantDialog(),
		workspaceDialog:      dialog.NewWorkspaceDialog(),
		subagentFullDlg:      dialog.NewSubagentFullDialog(),
		skillDialog:          dialog.NewSkillDialog(),
		provSetupDialog:      dialog.NewProviderSetupDialog(),
		overrideDialog:       dialog.NewOverrideConfirmDialog(),
		pluginsDialog:        dialog.NewPluginsDialog(),
		browserDialog:        dialog.NewBrowserDialog(),
		subagentFooterCursor: -1,
		filesPanel:           sidebar.NewFilesPanel(),
		costPanel:            sidebar.NewCostPanel(),
		sessionPanel:         sidebar.NewSessionPanel(),
	}

	m.helpDialog.SetBindings(AllBindings())
	m.helpDialog.SetDialogs(builtinHelpDialogs())
	m.helpDialog.SetAbout(dialog.HelpAbout{
		Version:   "0.1.0-dev",
		BuildDate: "dev",
		License:   "MIT / Apache-2.0",
		DocsURL:   "https://github.com/Sahaj-Tech-ltd/overkill",
	})

	m.sidebar.SetPanels([]sidebar.Panel{&m.costPanel, &m.filesPanel, &m.sessionPanel})

	// Build chat page first so registerCommands can wire its editor
	// (autocomplete entries) without a nil deref.
	m.chatPage = page.NewChatPage(nil)
	m.registerCommands()

	if app != nil {
		m.app = app
		// Initial footer-indicator sync; refreshed on each subagent tick.
		m.refreshBackendIndicators()
		if app.Agent != nil {
			m.chatPage = page.NewChatPage(app.Agent)
			// Install approval callback so risky tool calls open the permission dialog.
			app.Agent.SetApprovalFunc(m.makeApprovalFunc())
			app.Agent.SetQuestionFunc(m.makeQuestionFunc())
			// Plugin bridge: per-turn context + lifecycle events. No-op if
			// no plugins are loaded.
			if app.Plugins != nil {
				app.Agent.SetContextProvider(func(ctx context.Context, sessionID string) string {
					snippets := app.Plugins.Provide(ctx, "", sessionID)
					return plugin.AssembleSnippets(snippets)
				})
				app.Agent.SetEventFn(func(event string, payload map[string]any) {
					app.Plugins.FireEvent(event, payload)
				})
			}
		}
		// Open the user-level stash store (best-effort; stash is not critical).
		if path, err := session.DefaultStashPath(); err == nil {
			if store, err := session.NewStashStore(path); err == nil {
				m.stashStore = store
			}
		}
		if app.Config != nil {
			home, _ := os.UserHomeDir()
			if !onboarding.HasOnboarded(home) {
				ob := onboarding.New(app.Config)
				m.onboarding = &ob
				// Suppress the legacy first-run setup dialog — onboarding
				// covers the same ground (and more) on its own.
				m.firstRunSetup = false
				m.setupDialog.Show = false
			} else if len(app.Config.Providers) == 0 {
				m.firstRunSetup = true
				m.setupDialog.Show = true
				m.openDialog = dialogConfig
			}
			if app.Config.Agent.DefaultModel != "" {
				m.statusBar.SetModel(app.Config.Agent.DefaultModel, app.Config.Agent.DefaultProvider)
			}
			// Model list is sourced live from models.dev when the picker opens —
			// see fetchModelCatalogCmd / ctrl+o handler. No seeding here.
			var lvl personality.Level
			switch app.Config.Personality.Level {
			case "witty":
				lvl = personality.LevelWitty
			case "full":
				lvl = personality.LevelFull
			case "off":
				lvl = personality.LevelOff
			default:
				lvl = personality.LevelSubtle
			}
			m.person = personality.New(personality.Config{
				AgentName: app.Config.Agent.Name,
				Level:     lvl,
			})
		}
		// Sessions are created lazily on the first user turn so the welcome
		// screen doesn't list a phantom empty session.
	}

	return m
}

// bootstrapSession creates a fresh session record (if a store is configured)
// so the chat history is persisted as the user works.
func (m *appModel) bootstrapSession() {
	if m.app == nil || m.app.Store == nil {
		return
	}
	cwd, _ := os.Getwd()
	s := session.NewSession(cwd)
	s.Title = "session " + time.Now().Format("2006-01-02 15:04")
	if m.app.Config != nil {
		s.Model = m.app.Config.Agent.DefaultModel
		s.Provider = m.app.Config.Agent.DefaultProvider
	}
	if err := m.app.Store.Create(context.Background(), s); err == nil {
		m.currentSession = s
		m.installSessionHistory(s.ID)
	}
}

// installSessionHistory swaps the editor's prompt history sidecar to one
// persisted at ~/.overkill/sessions/<id>/prompt-history.txt.
func (m *appModel) installSessionHistory(sessionID string) {
	if sessionID == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	path := filepath.Join(home, ".overkill", "sessions", sessionID, "prompt-history.txt")
	if editor := m.chatPage.Editor(); editor != nil {
		editor.SetHistory(chat.NewHistoryWithFile(path))
	}
}

// makeApprovalFunc builds the agent.ApprovalFunc that bridges into the bubbletea
// loop via PermissionRequestMsg + a synchronous reply channel.
func (m *appModel) makeApprovalFunc() agent.ApprovalFunc {
	return func(toolName, args, risk string) agent.Approval {
		reply := make(chan tuitypes.PermissionReply, 1)
		// Ship the request into the TUI. We can't directly Send to bubbletea
		// from here without a Program reference, so we rely on the tea program
		// already being running and dispatch via the global program proxy held
		// in the package-level proxy variable installed by tea.NewProgram.
		programRef.Send(tuitypes.PermissionRequestMsg{
			ToolName: toolName,
			Args:     args,
			Risk:     risk,
			Reply:    reply,
		})
		select {
		case ans := <-reply:
			return agent.Approval{Allow: ans.Allow, Persist: ans.Persist}
		case <-time.After(2 * time.Minute):
			return agent.Approval{Allow: false}
		}
	}
}

// makeQuestionFunc bridges agent.AskQuestion calls into the TUI question
// dialog using the same channel-based pattern as approvals.
func (m *appModel) makeQuestionFunc() agent.QuestionFunc {
	return func(ctx context.Context, q agent.Question) agent.Answer {
		reply := make(chan tuitypes.QuestionReply, 1)
		programRef.Send(tuitypes.QuestionRequestMsg{
			Prompt:  q.Prompt,
			Choices: q.Choices,
			Reply:   reply,
		})
		select {
		case ans := <-reply:
			return agent.Answer{Text: ans.Text, Index: ans.Index, Cancel: ans.Cancel}
		case <-ctx.Done():
			return agent.Answer{Cancel: true}
		case <-time.After(5 * time.Minute):
			return agent.Answer{Cancel: true}
		}
	}
}

// programRef is set by SetProgram so the agent goroutine can deliver
// PermissionRequestMsg into the TUI loop. It's a tiny indirection layer to
// avoid passing *tea.Program through every call site.
var programRef programSender = noopSender{}

type programSender interface{ Send(tea.Msg) }
type noopSender struct{}

func (noopSender) Send(tea.Msg) {}

// SetProgram is called by the launcher after tea.NewProgram so the TUI can
// post messages to itself from background goroutines (e.g., approval prompts).
func SetProgram(p *tea.Program) {
	if p != nil {
		programRef = p
	}
}

// bootAutoDismissMsg dismisses the splash screen automatically so the editor
// becomes usable even if the user hasn't pressed a key yet.
type bootAutoDismissMsg struct{}

// sidebarFilesMsg carries the result of an async git status read.
type sidebarFilesMsg []sidebar.FileEntry

// refreshFilesCmd runs `git diff --numstat HEAD` and `git status --porcelain`
// in a goroutine so the UI thread is never blocked. Result arrives as
// sidebarFilesMsg on the next render tick.
func refreshFilesCmd() tea.Cmd {
	return func() tea.Msg {
		cwd, _ := os.Getwd()
		byPath := map[string]*sidebar.FileEntry{}
		if out, err := runGit(cwd, "diff", "--numstat", "HEAD"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				fields := strings.Fields(line)
				if len(fields) < 3 {
					continue
				}
				added, _ := atoiSafe(fields[0])
				deleted, _ := atoiSafe(fields[1])
				path := fields[2]
				byPath[path] = &sidebar.FileEntry{
					Path:    path,
					Added:   added,
					Deleted: deleted,
					Status:  "modified",
				}
			}
		}
		if out, err := runGit(cwd, "status", "--porcelain"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if len(line) < 4 {
					continue
				}
				code := strings.TrimSpace(line[:2])
				path := strings.TrimSpace(line[3:])
				st := "modified"
				switch {
				case strings.HasPrefix(code, "A"):
					st = "added"
				case strings.HasPrefix(code, "D"):
					st = "deleted"
				case code == "??":
					st = "untracked"
				}
				if existing, ok := byPath[path]; ok {
					existing.Status = st
				} else {
					byPath[path] = &sidebar.FileEntry{Path: path, Status: st}
				}
			}
		}
		entries := make([]sidebar.FileEntry, 0, len(byPath))
		for _, e := range byPath {
			entries = append(entries, *e)
		}
		return sidebarFilesMsg(entries)
	}
}

const bootAutoDismissDelay = 2 * time.Second

func (m *appModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.chatPage.Init(),
		m.statusBar.Init(),
		m.setupDialog.Init(),
		LoadBootData(m.person),
		tickSidebar(),
		tickSubagent(),
		tea.Tick(bootAutoDismissDelay, func(time.Time) tea.Msg { return bootAutoDismissMsg{} }),
		// Trigger an initial sidebar refresh so the files/cost/sessions tabs
		// show real data the moment the user opens the chat.
		func() tea.Msg { return tuitypes.SidebarRefreshMsg{} },
	}
	// Surface pending journal alerts as toasts on boot. Best-effort; failure
	// here never blocks the TUI from loading.
	if m.app != nil && m.app.Alerts != nil {
		pending := m.app.Alerts.Pending()
		for _, a := range pending {
			text := "[" + string(a.Type) + "] " + a.Message
			cmds = append(cmds, m.toastCmd(text, "warning"))
		}
		if len(pending) > 0 {
			_ = m.app.Alerts.DismissAll()
		}
	}
	return tea.Batch(cmds...)
}

func tickSidebar() tea.Cmd {
	return tea.Tick(sidebarRefreshInterval, func(time.Time) tea.Msg {
		return tuitypes.SidebarRefreshMsg{}
	})
}

func tickSubagent() tea.Cmd {
	return tea.Tick(subagentTickInterval, func(time.Time) tea.Msg {
		return tuitypes.SubagentTickMsg{}
	})
}

// builtinHelpDialogs returns the static catalog of dialogs the user can open
// from the help overlay. Action is left blank because dialogs are launched
// through their existing entry points (commands or keybindings).
func builtinHelpDialogs() []dialog.HelpEntry {
	return []dialog.HelpEntry{
		{Label: "Commands palette", Detail: "ctrl+k or /"},
		{Label: "Models picker", Detail: "ctrl+o or /model"},
		{Label: "Sessions list", Detail: "ctrl+s or /sessions"},
		{Label: "Theme picker", Detail: "ctrl+t or /theme"},
		{Label: "Config wizard", Detail: "ctrl+, / f2 or /config"},
		{Label: "Status panel", Detail: "ctrl+i or /status"},
		{Label: "Fork timeline", Detail: "ctrl+f or /fork"},
		{Label: "Diff viewer", Detail: "/diff <path>  (s = side-by-side)"},
		{Label: "MCP servers", Detail: "/mcp"},
		{Label: "Plugins", Detail: "/plugins"},
		{Label: "Worktrees", Detail: "/worktree"},
		{Label: "Permissions ledger", Detail: "/permissions"},
		{Label: "Tags browser", Detail: "/tags"},
		{Label: "Variant picker", Detail: "/variant"},
		{Label: "Workspace switcher", Detail: "/workspace"},
		{Label: "Subagent detail", Detail: "/subagents"},
		{Label: "Skills manager", Detail: "/skills"},
		{Label: "File mention picker", Detail: "@ in editor"},
		{Label: "Stash", Detail: "/stash"},
	}
}

// refreshHelpDynamic re-snapshots the dynamic help sections (Plugins / MCP /
// LSP) from the live App state. Called right before opening the dialog so
// the user always sees the current process state.
func (m *appModel) refreshHelpDynamic() {
	m.helpDialog.SetCommands(m.cmdDialog.Commands)
	if m.app == nil {
		m.helpDialog.SetPlugins(nil)
		m.helpDialog.SetMCP(nil)
		m.helpDialog.SetLSP(nil)
		return
	}
	if m.app.Plugins != nil {
		var entries []dialog.HelpEntry
		for _, s := range m.app.Plugins.Status() {
			state := "running"
			if !s.Running {
				state = "stopped"
			}
			if s.Disabled {
				state = "disabled"
			}
			entries = append(entries, dialog.HelpEntry{
				Label:  s.Name,
				Detail: fmt.Sprintf("%s · %d tools · %d commands", state, s.Tools, s.Commands),
			})
		}
		for _, c := range m.app.Plugins.Commands() {
			entries = append(entries, dialog.HelpEntry{
				Label:  "/" + c.Command.ID,
				Detail: c.Plugin + " · " + c.Command.Description,
			})
		}
		m.helpDialog.SetPlugins(entries)
	} else {
		m.helpDialog.SetPlugins(nil)
	}
	if m.app.MCP != nil {
		var entries []dialog.HelpEntry
		for _, s := range m.app.MCP.Status() {
			state := "connected"
			if !s.Connected {
				state = "disconnected"
			}
			entries = append(entries, dialog.HelpEntry{
				Label:  s.Name,
				Detail: fmt.Sprintf("%s · %d tools", state, s.ToolsCount),
			})
		}
		for _, tw := range m.app.MCP.Tools() {
			entries = append(entries, dialog.HelpEntry{
				Label:  tw.Server + ":" + tw.Tool.Name,
				Detail: "MCP tool",
			})
		}
		m.helpDialog.SetMCP(entries)
	} else {
		m.helpDialog.SetMCP(nil)
	}
	if m.app.LSP != nil {
		var entries []dialog.HelpEntry
		for _, lang := range m.app.LSP.Languages() {
			entries = append(entries, dialog.HelpEntry{
				Label:  lang,
				Detail: "language server",
			})
		}
		m.helpDialog.SetLSP(entries)
	} else {
		m.helpDialog.SetLSP(nil)
	}
}

// registerCommands populates the slash-command palette with the built-ins.
func (m *appModel) registerCommands() {
	m.cmdDialog = dialog.NewCommandDialog()
	for _, c := range []dialog.Command{
		{ID: "help", Title: "/help", Description: "show keybinding help"},
		{ID: "clear", Title: "/clear", Description: "clear chat history"},
		{ID: "quit", Title: "/quit", Description: "exit overkill"},
		{ID: "model", Title: "/model", Description: "open model picker"},
		{ID: "sessions", Title: "/sessions", Description: "switch session"},
		{ID: "theme", Title: "/theme", Description: "open theme picker"},
		{ID: "config", Title: "/config", Description: "reconfigure provider"},
		{ID: "compact", Title: "/compact", Description: "compact chat history"},
		{ID: "init", Title: "/init", Description: "write a starter .overkill/ config"},
		{ID: "status", Title: "/status", Description: "show provider, model, session status"},
		{ID: "fork", Title: "/fork", Description: "fork the conversation from a past message"},
		{ID: "stash", Title: "/stash", Description: "stash the current draft (or 'list' to browse)"},
		{ID: "diff", Title: "/diff", Description: "show a unified diff for a path"},
		{ID: "mcp", Title: "/mcp", Description: "show MCP server status & tools"},
		{ID: "browser", Title: "/browser", Description: "show agentic browser status"},
		{ID: "plugins", Title: "/plugins", Description: "show installed plugins & status"},
		{ID: "worktree", Title: "/worktree", Description: "manage git worktrees"},
		{ID: "permissions", Title: "/permissions", Description: "view permission ledger"},
		{ID: "tags", Title: "/tags", Description: "browse file tags"},
		{ID: "variant", Title: "/variant", Description: "compare a prompt across models"},
		{ID: "workspace", Title: "/workspace", Description: "switch between projects"},
		{ID: "subagents", Title: "/subagents", Description: "open subagent detail view"},
		{ID: "skills", Title: "/skills", Description: "manage installed skills"},
		{ID: "view", Title: "/view", Description: "open a file in the split-view pane"},
		{ID: "sync", Title: "/sync", Description: "push/pull session sync to remote backend"},
		{ID: "share", Title: "/share", Description: "share current session as a public URL"},
		{ID: "acp", Title: "/acp", Description: "show ACP server status & token"},
		{ID: "walls", Title: "/walls", Description: "run safety walls (architecture/test-quality/ouroboros)"},
		{ID: "routine", Title: "/routine", Description: "list automation routines"},
		{ID: "cron", Title: "/cron", Description: "list scheduled cron jobs"},
		{ID: "introspect", Title: "/introspect", Description: "regenerate codebase wiki snippet"},
		{ID: "slice", Title: "/slice", Description: "decompose a spec into vertical slices"},
		{ID: "diagnose", Title: "/diagnose", Description: "run diagnostic file analyzer on cwd"},
		{ID: "plan", Title: "/plan", Description: "draft a plan from the current goal"},
		{ID: "journal", Title: "/journal", Description: "search the flight-recorder journal"},
		{ID: "redteam", Title: "/redteam", Description: "run red-team checks on the active session"},
		{ID: "rollback", Title: "/rollback", Description: "list/restore filesystem checkpoints"},
		{ID: "orders", Title: "/orders", Description: "list active standing orders"},
		{ID: "mode", Title: "/mode", Description: "toggle reader/writer privilege mode"},
		{ID: "conceal", Title: "/conceal", Description: "toggle raw markdown rendering for clean copy-paste"},
		{ID: "usage", Title: "/usage", Description: "show cost + token usage for the active session"},
	} {
		m.cmdDialog.RegisterCommand(c)
	}
	// Mirror the slash commands into the inline autocomplete dropdown so
	// /-prefixed typing in the editor pops a non-modal hint list.
	editor := m.chatPage.Editor()
	if editor != nil {
		entries := make([]chat.AutocompleteEntry, 0, len(m.cmdDialog.Commands))
		for _, c := range m.cmdDialog.Commands {
			entries = append(entries, chat.AutocompleteEntry{
				ID: c.ID, Title: c.Title, Desc: c.Description,
			})
		}
		editor.SetAutocompleteEntries(entries)
	}
}

func (m *appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Onboarding completion — clear the wizard and (if a config was returned)
	// hot-swap into the new agent. The wizard wrote ~/.overkill/onboarded so we
	// won't see this branch on next launch.
	if cm, ok := msg.(onboarding.CompleteMsg); ok {
		m.onboarding = nil
		if cm.Config != nil && m.app != nil && len(cm.Config.Providers) > 0 {
			m.app.Config = cm.Config
			if newAgent, err := m.app.Reconfigure(cm.Config); err == nil && newAgent != nil {
				m.chatPage.SetAgent(newAgent)
				newAgent.SetApprovalFunc(m.makeApprovalFunc())
				m.statusBar.SetModel(cm.Config.Agent.DefaultModel, cm.Config.Agent.DefaultProvider)
			}
		}
		return m, nil
	}

	// While onboarding is active, route every key into the wizard. Window
	// size still flows through so we resize correctly mid-flow.
	if m.onboarding != nil {
		if ws, ok := msg.(tea.WindowSizeMsg); ok {
			m.width = ws.Width
			m.height = ws.Height - 1
			m.onboarding.SetSize(m.width, m.height)
			return m, nil
		}
		if _, ok := msg.(tea.KeyMsg); ok {
			updated, cmd := m.onboarding.Update(msg)
			m.onboarding = &updated
			return m, cmd
		}
	}

	// Handle setup-saved at any time — first-run quits, runtime hot-swaps.
	if saved, ok := msg.(dialog.SetupSavedMsg); ok {
		if err := dialog.SaveToConfig(saved); err == nil && !m.firstRunSetup && m.app != nil {
			if m.app.Config != nil {
				m.app.Config.Agent.DefaultProvider = saved.Provider
				m.app.Config.Agent.DefaultModel = saved.Model
			}
			if newAgent, err := m.app.Reconfigure(m.app.Config); err == nil && newAgent != nil {
				m.chatPage.SetAgent(newAgent)
				newAgent.SetApprovalFunc(m.makeApprovalFunc())
				m.statusBar.SetModel(saved.Model, saved.Provider)
			}
		}
		if m.firstRunSetup {
			return m, tea.Quit
		}
		m.openDialog = dialogNone
		m.setupDialog.Show = false
		return m, m.toastCmd("config saved", "success")
	}

	if _, ok := msg.(dialog.CloseSetupDialogMsg); ok {
		m.setupDialog.Show = false
		if m.openDialog == dialogConfig {
			m.openDialog = dialogNone
		}
		return m, nil
	}

	switch ev := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = ev.Width
		m.height = ev.Height - 1

		chatWidth := m.width
		if m.showSidebar && m.width >= 60 && m.chatPage.HasMessages() {
			m.sidebar.SetSize(defaultSidebarWidth, m.height)
			chatWidth = m.width - defaultSidebarWidth
		}
		// Split view shrinks chat to 60% and reserves 40% for the file viewer.
		if m.splitOpen && m.splitView != nil && m.width >= 80 {
			chatWidth = (m.width * 6) / 10
			m.splitView.SetSize(m.width-chatWidth, m.height)
		}

		var statusCmd tea.Cmd
		m.statusBar, statusCmd = m.statusBar.Update(tea.WindowSizeMsg{Width: m.width})
		chatCmd := m.sendToChat(tea.WindowSizeMsg{Width: chatWidth, Height: m.height})
		return m, tea.Batch(statusCmd, chatCmd)

	case BootCompleteMsg:
		m.boot.soulMD = ev.SoulMD
		m.boot.funFact = ev.FunFact
		m.boot.width = m.width
		m.boot.height = m.height
		m.boot.ready = true
		bootCmd := m.boot.StartBootAnimation()
		return m, bootCmd

	case bootAutoDismissMsg:
		m.boot.visible = false
		m.boot.StopBootAnimation()
		return m, nil

	case bootFadeTickMsg, bootTypeTickMsg, bootBlinkTickMsg, logo.ShimmerTickMsg:
		if m.boot.visible {
			var cmd tea.Cmd
			m.boot, cmd = m.boot.UpdateBoot(ev)
			return m, cmd
		}
		return m, nil

	case tuitypes.SidebarRefreshMsg:
		// Cost + session reads are in-memory / single store call — cheap, run
		// inline. Git status takes hundreds of ms — push that off the UI
		// thread via tea.Cmd so SSH redraws never freeze waiting on git.
		m.refreshCost()
		m.refreshSessionList()
		return m, tea.Batch(refreshFilesCmd(), tickSidebar())

	case sidebarFilesMsg:
		m.filesPanel.UpdateFilesPublic(ev)
		return m, nil

	case tuitypes.SubagentTickMsg:
		// Subagent footer redraws on each tick; refresh MCP/LSP indicator
		// counters here too (cheap snapshot reads, no I/O).
		m.refreshBackendIndicators()
		return m, tickSubagent()

	case tuitypes.ToastMsg:
		var cmd tea.Cmd
		m.toast, cmd = m.toast.Update(status.ShowMsgFromText(ev.Text))
		return m, cmd

	case tuitypes.PermissionRequestMsg:
		m.permissionDialog.SetRequest(ev)
		m.openDialog = dialogPermission
		return m, nil

	case dialog.PermissionDecisionMsg:
		m.openDialog = dialogNone
		return m, nil

	case tuitypes.QuestionRequestMsg:
		m.questionDialog.SetRequest(ev)
		m.openDialog = dialogQuestion
		return m, nil

	case dialog.QuestionDecisionMsg:
		m.openDialog = dialogNone
		return m, nil

	case dialog.StashSelectedMsg:
		m.openDialog = dialogNone
		// Insert the stashed text into the editor and remove from store.
		if editor := m.chatPage.Editor(); editor != nil {
			editor.SetValue(ev.Entry.Text)
		}
		if m.stashStore != nil {
			_ = m.stashStore.Delete(ev.Entry.ID)
		}
		return m, m.toastCmd("stash restored", "info")

	case dialog.CloseStashDialogMsg:
		m.openDialog = dialogNone
		return m, nil

	case dialog.FileMentionSelectedMsg:
		m.openDialog = dialogNone
		// Splice "@<path> " into the editor at the trailing @ position.
		if editor := m.chatPage.Editor(); editor != nil {
			val := editor.Value()
			// Strip the trailing @<query> the user was typing.
			if idx := strings.LastIndex(val, "@"); idx >= 0 {
				val = val[:idx]
			}
			val += "@" + ev.Path + " "
			editor.SetValue(val)
		}
		return m, nil

	case dialog.CloseFileMentionMsg:
		m.openDialog = dialogNone
		return m, nil

	case dialog.CloseDiffDialogMsg:
		m.openDialog = dialogNone
		return m, nil

	case dialog.CloseMCPDialogMsg, dialog.CloseWorktreeDialogMsg, dialog.CloseBrowserDialogMsg:
		m.openDialog = dialogNone
		return m, nil

	case dialog.MCPRescanMsg:
		return m, m.applyMCPRescan()

	case dialog.BrowserCloseMsg:
		if m.app != nil && m.app.Browser != nil {
			m.app.Browser.Close()
			m.browserDialog.SetStatus(m.app.Browser.Status())
		}
		return m, m.toastCmd("browser: closed", "success")

	case dialog.BrowserRefreshMsg:
		if m.app != nil && m.app.Browser != nil {
			if err := m.app.Browser.Refresh(); err != nil {
				return m, m.toastCmd("browser: "+err.Error(), "warning")
			}
			m.browserDialog.SetStatus(m.app.Browser.Status())
		}
		return m, nil

	case dialog.BrowserScreenshotMsg:
		return m, m.toastCmd("use: overkill browser test <url> for one-shot capture", "info")

	case dialog.WorktreeAddRequestMsg:
		// Surface a hint instead of inline input — `/worktree add` is the
		// scripted path so the agent can review before risky changes.
		return m, m.toastCmd("type: /worktree add <path> [branch]", "info")

	case dialog.CommandSelectedMsg:
		return m, m.dispatchCommand(ev.Command.ID)

	case dialog.HelpEntrySelectedMsg:
		// Help dialog forwarded an actionable entry. Action equals the slash
		// command id when the entry was sourced from the Commands section.
		m.openDialog = dialogNone
		m.helpDialog.Show = false
		if ev.Entry.Action != "" {
			return m, m.dispatchCommand(ev.Entry.Action)
		}
		return m, nil

	case variantResultsMsg:
		m.variantDialog.SetResults(ev.Results)
		m.variantDialog.Show = true
		m.openDialog = dialogVariant
		return m, nil

	case dialog.VariantPickedMsg:
		m.openDialog = dialogNone
		// Inject the picked response into the chat history as an assistant
		// message and clear the editor draft.
		if m.app != nil && m.app.Agent != nil {
			m.app.Agent.Inject(providers.Message{Role: "assistant", Content: ev.Response})
			m.chatPage.AppendRaw("assistant", ev.Response)
		}
		if editor := m.chatPage.Editor(); editor != nil {
			editor.SetValue("")
		}
		return m, m.toastCmd("picked variant: "+ev.Model, "success")

	case dialog.CloseVariantDialogMsg, dialog.ClosePermissionsLedgerMsg,
		dialog.CloseTagDialogMsg, dialog.CloseWorkspaceDialogMsg,
		dialog.CloseSubagentFullDialogMsg, dialog.CloseSkillDialogMsg,
		dialog.ClosePluginsDialogMsg:
		m.openDialog = dialogNone
		return m, nil

	case dialog.PluginToggleMsg:
		return m, m.applyPluginToggle(ev.Name)

	case dialog.TagSelectedMsg:
		m.openDialog = dialogNone
		// Insert "@path1 @path2 ..." mentions into the editor.
		if editor := m.chatPage.Editor(); editor != nil {
			val := editor.Value()
			var b strings.Builder
			b.WriteString(val)
			if val != "" && !strings.HasSuffix(val, " ") {
				b.WriteString(" ")
			}
			for i, p := range ev.Paths {
				if i > 0 {
					b.WriteString(" ")
				}
				b.WriteString("@" + p)
			}
			b.WriteString(" ")
			editor.SetValue(b.String())
		}
		return m, m.toastCmd(fmt.Sprintf("inserted %d files", len(ev.Paths)), "info")

	case dialog.WorkspaceSwitchMsg:
		m.openDialog = dialogNone
		return m, m.applyWorkspaceSwitch(ev.ID)

	case dialog.WorkspaceAddMsg:
		// Add cwd as a new workspace; reopen the dialog to show it.
		if m.app != nil && m.app.Workspace != nil {
			cwd, _ := os.Getwd()
			if _, err := m.app.Workspace.Add(cwd, filepath.Base(cwd)); err == nil {
				m.workspaceDialog.SetWorkspaces(m.app.Workspace.List())
			}
		}
		return m, m.toastCmd("added workspace", "success")

	case dialog.SkillToggleMsg:
		// Mirror the dialog's in-memory toggle into App.Skills, then persist
		// the resulting enabled list into config.toml.
		if m.app != nil {
			for i, s := range m.app.Skills {
				if s.Name == ev.Name {
					m.app.Skills[i].Enabled = ev.Enabled
					break
				}
			}
			verb := "disabled"
			if ev.Enabled {
				verb = "enabled"
			}
			cmds := []tea.Cmd{m.toastCmd("skill "+ev.Name+" "+verb, "success")}
			if c := m.persistSkillEnabled(); c != nil {
				cmds = append(cmds, c)
			}
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case dialog.ThemeSelectedMsg:
		m.openDialog = dialogNone
		return m, m.toastCmd("theme applied", "success")

	case tuitypes.ModelCatalogLoadedMsg:
		if ev.Catalog != nil {
			m.modelDialog.SetCatalog(ev.Catalog, ev.Source)
		} else {
			m.modelDialog.SetLoading(false)
		}
		return m, nil

	case dialog.ModelSelectedMsg:
		m.openDialog = dialogNone
		return m, m.applyModelSelection(ev.ModelID, ev.Provider)

	case dialog.ProviderConfiguredMsg:
		m.openDialog = dialogNone
		return m, m.applyProviderConfigured(ev)

	case dialog.CloseProviderSetupMsg:
		m.openDialog = dialogNone
		return m, nil

	case dialog.OverrideConfirmMsg:
		m.openDialog = dialogNone
		switch ev.Choice {
		case dialog.OverrideJustSwitch:
			// Hot-swap to the new provider+model using the stored credentials.
			if m.app != nil && m.app.Config != nil {
				m.app.Config.Agent.DefaultProvider = ev.Provider
				m.app.Config.Agent.DefaultModel = ev.Model
				if newAgent, err := m.app.Reconfigure(m.app.Config); err == nil && newAgent != nil {
					m.chatPage.SetAgent(newAgent)
					newAgent.SetApprovalFunc(m.makeApprovalFunc())
				}
				m.statusBar.SetModel(ev.Model, ev.Provider)
			}
			return m, m.toastCmd("switched to "+ev.Model, "success")
		case dialog.OverrideUpdateCreds:
			// Pre-fill the wizard with the user's existing key + endpoint
			// so they only edit what's actually changing (e.g. rotated key).
			existingKey, existingURL := m.lookupProviderCreds(ev.Provider)
			m.provSetupDialog.OpenWithExisting(ev.Provider, ev.Model, defaultBaseURLFor(ev.Provider), existingKey, existingURL)
			m.openDialog = dialogProviderSetup
			return m, nil
		case dialog.OverrideCancel:
			return m, nil
		}
		return m, nil

	case dialog.CloseOverrideConfirmMsg:
		m.openDialog = dialogNone
		return m, nil

	case dialog.SessionSelectedMsg:
		m.openDialog = dialogNone
		return m, m.applySessionSelection(ev.Session)

	case dialog.SessionRenameRequestMsg:
		m.renameDialog.SetSession(ev.Session)
		m.openDialog = dialogRename
		return m, nil

	case dialog.SessionDeleteRequestMsg:
		m.deleteSession = ev.Session
		m.openDialog = dialogDeleteConfirm
		return m, nil

	case dialog.SessionNewRequestMsg:
		m.openDialog = dialogNone
		return m, m.startNewSession()

	case dialog.SessionRenamedMsg:
		m.openDialog = dialogSessions
		if ev.Session != nil && ev.Title != "" && m.app != nil && m.app.Store != nil {
			ev.Session.Title = ev.Title
			_ = m.app.Store.Save(context.Background(), ev.Session)
			m.refreshSessionList()
			return m, m.toastCmd("session renamed", "success")
		}
		return m, nil

	case dialog.CloseRenameDialogMsg:
		m.openDialog = dialogSessions
		return m, nil

	case dialog.TimelineForkMsg:
		m.openDialog = dialogNone
		return m, m.applyFork(ev.KeepCount)

	case dialog.CloseTimelineDialogMsg, dialog.CloseStatusDialogMsg:
		m.openDialog = dialogNone
		return m, nil

	case dialog.CloseCommandDialogMsg, dialog.CloseModelDialogMsg,
		dialog.CloseSessionDialogMsg, dialog.CloseThemeDialogMsg,
		dialog.CloseHelpMsg:
		m.openDialog = dialogNone
		return m, nil

	case tuitypes.SendMsg:
		// Slash commands typed in the editor should dispatch instead of
		// being sent to the LLM as plain chat. Match exact ID first, then
		// fall back to a unique prefix match so /models, /mod, /sess all
		// resolve. Unknown or ambiguous: toast and don't send.
		if strings.HasPrefix(ev.Text, "/") {
			fields := strings.Fields(ev.Text)
			name := strings.TrimSpace(strings.TrimPrefix(fields[0], "/"))
			args := strings.TrimSpace(strings.TrimPrefix(ev.Text, fields[0]))
			if id, ok := m.resolveSlashCommand(name); ok {
				return m, m.dispatchCommandWithArgs(id, args)
			}
			return m, m.toastCmd(fmt.Sprintf("unknown command: /%s", name), "warning")
		}
		// Surface "busy" state to the user instead of silently dropping input.
		if m.chatPage.IsBusy() {
			return m, m.toastCmd("agent busy, please wait", "warning")
		}
		return m, m.sendToChat(ev)

	case tuitypes.AgentResponseMsg, tuitypes.AgentStreamMsg:
		// The chat page is the source of truth for these — forward and persist
		// any newly completed turns.
		var cmd tea.Cmd
		cmd = m.sendToChat(ev)
		// Update status-bar spinner state based on stream lifecycle.
		if sm, ok := ev.(tuitypes.AgentStreamMsg); ok {
			st := tuitypes.StatusGenerating
			tool := sm.ToolName
			if tool != "" {
				st = tuitypes.StatusToolCall
			}
			if sm.Done {
				st = tuitypes.StatusIdle
			}
			m.statusBar, _ = m.statusBar.Update(status.StateChangeMsg{State: st, ToolName: tool})
		}
		// Update the busy/connection state.
		if sm, ok := ev.(tuitypes.AgentStreamMsg); ok {
			if sm.Err != nil {
				m.failStreak++
				if m.failStreak >= 2 {
					m.connState = 3
				} else {
					m.connState = 2
				}
				m.statusBar.SetConnState(m.connState)
				// Auth failures: open the provider setup wizard so the user
				// can re-enter the key without dropping to a terminal.
				if isAuthError(sm.Err) {
					cmd = tea.Batch(cmd, m.openProviderSetupForCurrent("auth failed — re-enter your api key"))
				}
			} else if sm.Done {
				m.failStreak = 0
				m.connState = 1
				m.statusBar.SetConnState(m.connState)
				m.persistSession()
				m.maybeAutoPushSync()
			}
		}
		return m, cmd

	case tea.KeyMsg:
		// Boot screen: dismiss on any keystroke, but forward it to the editor
		// so the first typed character isn't silently consumed.
		if m.boot.visible {
			if ev.String() == "ctrl+c" {
				return m, tea.Quit
			}
			m.boot.visible = false
			// Fall through — the keystroke continues to the editor below.
		}

		// ctrl+c: double-press to exit. First press cancels (placeholder for
		// in-flight cancel) and arms the exit; second press within 2s exits.
		if ev.String() == "ctrl+c" {
			now := time.Now()
			if !m.quitArmedAt.IsZero() && now.Sub(m.quitArmedAt) < 2*time.Second {
				return m, tea.Quit
			}
			m.quitArmedAt = now
			return m, m.toastCmd("press ctrl+c again to exit", "warning")
		}

		// esc: double-press to exit when no dialog is open.
		if ev.String() == "esc" && m.openDialog == dialogNone {
			now := time.Now()
			if !m.escArmedAt.IsZero() && now.Sub(m.escArmedAt) < 2*time.Second {
				return m, tea.Quit
			}
		m.escArmedAt = now
		return m, m.toastCmd("press esc again to exit", "warning")
	}

	// Any other key press disarms both exit triggers so stray typing doesn't
	// accidentally exit.
	if ev.String() != "ctrl+c" && ev.String() != "esc" {
		m.quitArmedAt = time.Time{}
		m.escArmedAt = time.Time{}
	}

	// Delete-confirm overlay.
		if m.openDialog == dialogDeleteConfirm {
			switch ev.String() {
			case "y", "Y":
				if m.deleteSession != nil && m.app != nil && m.app.Store != nil {
					_ = m.app.Store.Delete(context.Background(), m.deleteSession.ID)
					m.deleteSession = nil
					m.refreshSessionList()
				}
				m.openDialog = dialogSessions
				return m, m.toastCmd("session deleted", "success")
			case "n", "N", "esc":
				m.deleteSession = nil
				m.openDialog = dialogSessions
				return m, nil
			}
			return m, nil
		}

		// If a dialog is open, route keys to it (esc closes).
		if m.openDialog != dialogNone {
			return m.routeDialogKey(ev)
		}

		// Split-view key handling (when open). Esc closes; Tab cycles focus;
		// j/k/pgup/pgdn scroll the file viewer when it owns focus.
		if m.splitOpen && m.splitView != nil {
			switch ev.String() {
			case "esc":
				if m.splitFocused || m.openDialog == dialogNone {
					m.splitOpen = false
					m.splitFocused = false
					return m, func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height + 1} }
				}
			case "tab":
				if m.openDialog == dialogNone {
					m.splitFocused = !m.splitFocused
					m.splitView.SetFocus(m.splitFocused)
					return m, nil
				}
			}
			if m.splitFocused {
				switch ev.String() {
				case "j", "down":
					m.splitView.ScrollDown(1)
					return m, nil
				case "k", "up":
					m.splitView.ScrollUp(1)
					return m, nil
				case "pgdown", "ctrl+d":
					m.splitView.PageDown()
					return m, nil
				case "pgup", "ctrl+u":
					m.splitView.PageUp()
					return m, nil
				}
			}
		}

		// Subagent footer focus: ↑/↓ navigate when there are running children
		// AND the editor is empty AND no dialog is open. Enter opens the
		// detail dialog for the focused entry; esc returns focus to editor.
		if n := m.subagentChildCount(); n > 0 && m.editorEmpty() {
			switch ev.String() {
			case "up":
				if m.subagentFooterCursor < 0 {
					m.subagentFooterCursor = n - 1
				} else if m.subagentFooterCursor > 0 {
					m.subagentFooterCursor--
				}
				return m, nil
			case "down":
				if m.subagentFooterCursor < 0 {
					return m, nil
				}
				if m.subagentFooterCursor < n-1 {
					m.subagentFooterCursor++
				} else {
					m.subagentFooterCursor = -1
				}
				return m, nil
			case "esc":
				if m.subagentFooterCursor >= 0 {
					m.subagentFooterCursor = -1
					return m, nil
				}
			case "enter":
				if m.subagentFooterCursor >= 0 {
					children := m.app.Subagent.ActiveChildren()
					m.subagentFullDlg.SetChildren(children)
					m.subagentFullDlg.SetCursor(m.subagentFooterCursor)
					m.subagentFullDlg.Show = true
					m.openDialog = dialogSubagentFull
					return m, nil
				}
			}
		}

		// Global keybindings.
		switch ev.String() {
		case "ctrl+e":
			// Without an arg ctrl+e just toggles the existing split closed.
			if m.splitOpen {
				m.splitOpen = false
				m.splitFocused = false
				return m, func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height + 1} }
			}
			return m, m.toastCmd("type /view <path> to open the split", "info")
		case "ctrl+k":
			m.openDialog = dialogCommands
			m.cmdDialog.Show = true
			return m, nil
		case "ctrl+o":
			return m, m.openModelDialog()
		case "ctrl+s":
			m.refreshSessionList()
			m.openDialog = dialogSessions
			m.sessionDialog.Show = true
			return m, nil
		case "ctrl+t":
			m.openDialog = dialogTheme
			m.themeDialog, _ = m.themeDialog.Update(dialog.ShowThemeDialogMsg{})
			return m, nil
		case "ctrl+_", "f2":
			m.openDialog = dialogConfig
			m.setupDialog, _ = m.setupDialog.Update(dialog.ShowSetupDialogMsg{})
			return m, nil
		case "ctrl+h":
			m.refreshHelpDynamic()
			m.openDialog = dialogHelp
			m.helpDialog.Show = true
			return m, nil
		case "ctrl+i":
			m.statusDialog.SetInfo(m.collectStatusInfo())
			m.statusDialog.Show = true
			m.openDialog = dialogStatus
			return m, nil
		case "ctrl+f":
			if m.app != nil && m.app.Agent != nil {
				m.timelineDialog.SetMessages(m.app.Agent.History())
				m.openDialog = dialogTimeline
			}
			return m, nil
		}

		// Slash-trigger from empty editor opens command palette.
		if ev.String() == "/" && m.chatPage.Editor().Value() == "" {
			m.openDialog = dialogCommands
			m.cmdDialog.Show = true
			return m, nil
		}

		// `@` opens the file mention picker. We let the editor process the key
		// first (so the @ shows up in the editor) then open the dialog.
		if ev.String() == "@" {
			cwd, _ := os.Getwd()
			m.fileMentionDlg.LoadFromCwd(cwd, false)
			m.fileMentionDlg.SetQuery("")
			m.fileMentionDlg.Show = true
			m.openDialog = dialogFileMention
			return m, m.sendToChat(ev)
		}

		// Tab cycles sidebar tabs when the sidebar is visible.
		if ev.String() == "tab" && m.showSidebar && m.width >= 60 && m.chatPage.HasMessages() {
			sd, _ := m.sidebar.Update(ev)
			m.sidebar = sd
			return m, nil
		}

		return m, m.sendToChat(ev)

	default:
		var statusCmd tea.Cmd
		m.statusBar, statusCmd = m.statusBar.Update(msg)
		var toastCmd tea.Cmd
		m.toast, toastCmd = m.toast.Update(msg)
		chatCmd := m.sendToChat(msg)
		sd, sdCmd := m.sidebar.Update(msg)
		m.sidebar = sd
		return m, tea.Batch(statusCmd, chatCmd, sdCmd, toastCmd)
	}
}

// applyModelSelection swaps the model on the active agent, optionally
// reconfiguring the provider if the chosen model belongs to a different one.
// If the user has no API key configured for the new provider, the swap is
// refused and a toast nudges them toward /config.
func (m *appModel) applyModelSelection(modelID, provider string) tea.Cmd {
	if m.app == nil {
		return nil
	}
	if m.app.Agent != nil {
		sameProvider := provider == "" || m.app.Config == nil || provider == m.app.Config.Agent.DefaultProvider
		if sameProvider {
			m.app.Agent.SetModel(modelID)
			m.statusBar.SetModel(modelID, provider)
			return m.toastCmd("switched to "+modelID, "success")
		}

		// Cross-provider swap with no API key: open the focused wizard so
		// the user can paste a key and confirm the endpoint without leaving
		// the model picker context.
		if !m.providerConfigured(provider) {
			m.provSetupDialog.Open(provider, modelID, defaultBaseURLFor(provider))
			m.openDialog = dialogProviderSetup
			return nil
		}
		// Cross-provider swap with provider already configured: ask the user
		// whether to just switch, update credentials, or cancel. Lets them
		// rotate keys / change endpoint without leaving the model picker.
		m.overrideDialog.Open(provider, modelID)
		m.openDialog = dialogOverrideConfirm
		return nil
	}
	m.statusBar.SetModel(modelID, provider)
	return m.toastCmd("switched to "+modelID, "success")
}

// applyProviderConfigured persists the provider+key+endpoint chosen via the
// ProviderSetupDialog wizard, then swaps the agent over to the new model.
// Best-effort: a save failure still rebuilds the agent so the session is
// usable; the user just won't see the key persist on next launch.
func (m *appModel) applyProviderConfigured(ev dialog.ProviderConfiguredMsg) tea.Cmd {
	if m.app == nil || m.app.Config == nil {
		return m.toastCmd("config unavailable", "error")
	}
	cfg := m.app.Config

	// Upsert provider entry.
	found := false
	for i, p := range cfg.Providers {
		if p.Name == ev.Provider {
			cfg.Providers[i].APIKey = ev.APIKey
			if ev.BaseURL != "" {
				cfg.Providers[i].BaseURL = ev.BaseURL
			}
			if cfg.Providers[i].Type == "" {
				cfg.Providers[i].Type = ev.Provider
			}
			found = true
			break
		}
	}
	if !found {
		cfg.Providers = append(cfg.Providers, config.ProviderConfig{
			Name:    ev.Provider,
			Type:    ev.Provider,
			APIKey:  ev.APIKey,
			BaseURL: ev.BaseURL,
		})
	}
	cfg.Agent.DefaultProvider = ev.Provider
	cfg.Agent.DefaultModel = ev.Model

	// Persist (best-effort).
	if path, err := config.ConfigPath(); err == nil {
		_ = cfg.Save(path)
	}

	if newAgent, err := m.app.Reconfigure(cfg); err == nil && newAgent != nil {
		m.chatPage.SetAgent(newAgent)
		newAgent.SetApprovalFunc(m.makeApprovalFunc())
	}
	m.statusBar.SetModel(ev.Model, ev.Provider)
	return m.toastCmd("configured "+ev.Provider+" — switched to "+ev.Model, "success")
}

// lookupProviderCreds returns the currently-stored API key and base URL for
// the given provider name from cfg.Providers. Used to pre-fill the override
// wizard so the user only edits what's actually changing.
func (m *appModel) lookupProviderCreds(name string) (apiKey, baseURL string) {
	if m.app == nil || m.app.Config == nil {
		return "", ""
	}
	for _, p := range m.app.Config.Providers {
		if p.Name == name {
			return p.APIKey, p.BaseURL
		}
	}
	return "", ""
}

// isAuthError returns true if the error string looks like a 401/403 from the
// upstream provider. Used to auto-open the provider setup wizard so the user
// can fix credentials without dropping to a terminal.
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "401") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "authentication")
}

// openProviderSetupForCurrent opens the provider setup wizard pre-filled
// with the currently active provider/model so the user can fix credentials
// without losing context. Toasts the supplied reason first.
func (m *appModel) openProviderSetupForCurrent(reason string) tea.Cmd {
	if m.app == nil || m.app.Config == nil {
		return m.toastCmd(reason, "error")
	}
	provider := m.app.Config.Agent.DefaultProvider
	model := m.app.Config.Agent.DefaultModel
	if provider == "" {
		return m.toastCmd(reason, "error")
	}
	m.provSetupDialog.Open(provider, model, defaultBaseURLFor(provider))
	m.openDialog = dialogProviderSetup
	return m.toastCmd(reason, "warning")
}

// defaultBaseURLFor returns the canonical default endpoint for a provider id.
// Falls back to "" so the wizard's custom-URL path is the user's only option.
func defaultBaseURLFor(provider string) string {
	urls := map[string]string{
		"openai":     "https://api.openai.com/v1",
		"anthropic":  "https://api.anthropic.com",
		"gemini":     "https://generativelanguage.googleapis.com/v1beta",
		"deepseek":   "https://api.deepseek.com/v1",
		"ollama":     "http://localhost:11434",
		"openrouter": "https://openrouter.ai/api/v1",
		"groq":       "https://api.groq.com/openai/v1",
		"mistral":    "https://api.mistral.ai/v1",
	}
	return urls[provider]
}

// providerConfigured reports whether the given provider id has a non-empty
// APIKey in the user's config. Used to gate cross-provider model swaps.
func (m *appModel) providerConfigured(provider string) bool {
	if m.app == nil || m.app.Config == nil {
		return false
	}
	for _, p := range m.app.Config.Providers {
		if p.Name == provider && strings.TrimSpace(p.APIKey) != "" {
			return true
		}
	}
	return false
}

// openModelDialog opens the picker and kicks off a live fetch. The dialog
// shows a spinner immediately; the result lands as a ModelCatalogLoadedMsg.
// Resets dialog state so a re-open doesn't briefly flash the previous list.
func (m *appModel) openModelDialog() tea.Cmd {
	m.openDialog = dialogModels
	m.modelDialog.Show = true
	m.modelDialog.SetModels(nil) // clear stale rows from any prior open
	m.modelDialog.SetLoading(true)
	return fetchModelCatalogCmd()
}

// fetchModelCatalogCmd performs the network call off the UI goroutine and
// emits ModelCatalogLoadedMsg with the source label so the dialog can show
// a "(cached, offline)" hint when the live fetch failed.
func fetchModelCatalogCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), providers.FetchTimeout+time.Second)
		defer cancel()
		cat, err := providers.FetchCatalog(ctx)
		source := ""
		if cat != nil {
			source = string(cat.Source())
		}
		return tuitypes.ModelCatalogLoadedMsg{Catalog: cat, Source: source, Err: err}
	}
}

// applySessionSelection loads a saved session into the agent + chat page.
func (m *appModel) applySessionSelection(s *session.Session) tea.Cmd {
	if s == nil || m.app == nil || m.app.Agent == nil {
		return nil
	}
	m.app.Agent.SetHistory(s.Messages)
	m.chatPage.ClearHistory()
	for _, msg := range s.Messages {
		m.chatPage.AppendRaw(msg.Role, msg.Content)
	}
	m.currentSession = s
	m.installSessionHistory(s.ID)
	return m.toastCmd("session loaded", "info")
}

// applyWorkspaceSwitch chdirs to the picked workspace and reopens the
// session store at <path>/.overkill/sessions, swapping in the new store on
// success. The chdir + store reopen happen atomically inside SwitchWith
// so partial-failure leaves the old store intact.
func (m *appModel) applyWorkspaceSwitch(id string) tea.Cmd {
	if m.app == nil || m.app.Workspace == nil {
		return nil
	}
	var newStore *session.BadgerStore
	ws, err := m.app.Workspace.SwitchWith(id, func(w workspace.Workspace) error {
		dir := filepath.Join(w.Path, ".overkill", "sessions")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		s, err := session.NewBadgerStore(dir)
		if err != nil {
			return err
		}
		newStore = s
		return nil
	})
	if err != nil {
		return m.toastCmd("workspace: "+err.Error(), "error")
	}
	// Now safe to swap the store on App. Close the previous one (best-effort).
	if newStore != nil {
		if old, ok := m.app.Store.(*session.BadgerStore); ok && old != nil {
			_ = old.Close()
		}
		m.app.Store = newStore
		m.refreshSessionList()
	}
	count := 0
	if m.app.Store != nil {
		if list, err := m.app.Store.List(context.Background(), session.ListOptions{}); err == nil {
			count = len(list)
		}
	}
	return tea.Batch(
		refreshFilesCmd(),
		m.toastCmd(fmt.Sprintf("switched to %s · %d sessions", ws.Name, count), "success"),
	)
}

// persistSkillEnabled flushes the in-memory enabled set into config and
// writes it to disk via the resolved config path. Best-effort; failures are
// returned via tea.Cmd toast so the user sees them.
func (m *appModel) persistSkillEnabled() tea.Cmd {
	if m.app == nil || m.app.Config == nil {
		return nil
	}
	enabled := []string{}
	for _, s := range m.app.Skills {
		if s.Enabled {
			enabled = append(enabled, s.Name)
		}
	}
	m.app.Config.Skills.Enabled = enabled
	if m.app.ConfigPath == "" {
		return nil
	}
	if err := m.app.Config.Save(m.app.ConfigPath); err != nil {
		return m.toastCmd("skills: save failed: "+err.Error(), "error")
	}
	return nil
}

// applyFork keeps only the first N history messages, effectively rewinding.
func (m *appModel) applyFork(keep int) tea.Cmd {
	if m.app == nil || m.app.Agent == nil {
		return nil
	}
	hist := m.app.Agent.History()
	if keep > len(hist) {
		keep = len(hist)
	}
	if keep < 0 {
		keep = 0
	}
	m.app.Agent.SetHistory(hist[:keep])
	m.chatPage.ClearHistory()
	// Rehydrate chat page from agent history.
	for _, msg := range hist[:keep] {
		m.chatPage.AppendRaw(msg.Role, msg.Content)
	}
	return m.toastCmd(fmt.Sprintf("forked at message %d", keep), "info")
}

// startNewSession archives the current state and clears for a fresh one. The
// next turn lazily creates the session record on save.
func (m *appModel) startNewSession() tea.Cmd {
	if m.app != nil && m.app.Agent != nil {
		m.app.Agent.ClearHistory()
	}
	m.chatPage.ClearHistory()
	m.currentSession = nil
	return m.toastCmd("new session", "info")
}

// persistSession writes the current chat history into the active session
// record so the user can resume later. Lazily creates the session on the
// first turn so the welcome state never shows a phantom empty session.
func (m *appModel) persistSession() {
	if m.app == nil || m.app.Store == nil || m.app.Agent == nil {
		return
	}
	if m.currentSession == nil {
		m.bootstrapSession()
		if m.currentSession == nil {
			return
		}
	}
	hist := m.app.Agent.History()
	m.currentSession.Messages = hist
	m.currentSession.TurnCount = len(hist)
	if m.currentSession.Title == "" || strings.HasPrefix(m.currentSession.Title, "session ") {
		m.currentSession.Title = deriveTitle(hist)
	}
	_ = m.app.Store.Save(context.Background(), m.currentSession)
}

// deriveTitle picks a short label from the first user message in history.
func deriveTitle(hist []providers.Message) string {
	for _, m := range hist {
		if m.Role == "user" && m.Content != "" {
			t := strings.ReplaceAll(m.Content, "\n", " ")
			if len(t) > 60 {
				t = t[:57] + "..."
			}
			return t
		}
	}
	return "session"
}

// refreshSessionList pulls the latest session list from the store.
func (m *appModel) refreshSessionList() {
	if m.app == nil || m.app.Store == nil {
		return
	}
	cwd, _ := os.Getwd()
	sessions, err := m.app.Store.List(context.Background(), session.ListOptions{Folder: cwd, Limit: 50})
	if err != nil {
		return
	}
	m.sessionDialog.SetSessions(sessions)
	m.sessionPanel.SetSessions(sessions)
	if m.currentSession != nil {
		m.sessionPanel.SetCurrent(m.currentSession.ID)
	}
}

// Files refresh moved to refreshFilesCmd (async). Cost + session reads are
// fast in-memory / single store call so they stay inline at the call site.

// refreshBackendIndicators syncs the footer's MCP/LSP indicators with the
// current manager state. Nil-safe so it works even when neither backend is
// configured.
func (m *appModel) refreshBackendIndicators() {
	if m.app == nil {
		return
	}
	if m.app.MCP != nil {
		ok, failed := m.app.MCP.Counts()
		m.statusBar.SetMCPCount(ok, failed)
	}
	if m.app.LSP != nil {
		m.statusBar.SetLSPCount(m.app.LSP.ConnectedCount())
	}
	m.statusBar.SetBrowserActive(m.app.Browser != nil && m.app.Browser.IsActive())
}

// worktreeList delegates to internal/worktree.List, kept as a wrapper so
// the symbol is reachable from the TUI without a forward import cycle.
func worktreeList(dir string) ([]wt.Worktree, error) {
	return wt.List(dir)
}

// runGit runs `git <args>` in dir and returns stdout as a string.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// atoiSafe parses an integer, returning 0 on error rather than panicking.
func atoiSafe(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func (m *appModel) refreshCost() {
	if m.app == nil || m.app.Costs == nil {
		return
	}
	ctx := context.Background()
	if m.currentSession != nil {
		if sum, err := m.app.Costs.SessionCost(ctx, m.currentSession.ID); err == nil {
			m.costPanel.UpdateSummary(sum)
		}
	}
	if budget, err := m.app.Costs.CheckBudget(ctx, ""); err == nil {
		m.costPanel.UpdateBudget(budget)
	}
}

// collectStatusInfo gathers data for the /status overlay.
func (m *appModel) collectStatusInfo() dialog.StatusInfo {
	info := dialog.StatusInfo{ProviderOK: m.connState != 3}
	if m.app == nil {
		return info
	}
	if m.app.Config != nil {
		info.ProviderName = m.app.Config.Agent.DefaultProvider
		info.ModelID = m.app.Config.Agent.DefaultModel
		for _, p := range m.app.Config.Providers {
			if p.Name == m.app.Config.Agent.DefaultProvider {
				info.ProviderBaseURL = p.BaseURL
				break
			}
		}
	}
	if m.app.Agent != nil {
		hist := m.app.Agent.History()
		info.MessageCount = len(hist)
		if r := m.app.Agent.BudgetReport(); r != nil {
			info.MaxTokens = r.MaxTokens
			info.TotalTokens = int64(r.TotalEstimate)
		}
	}
	if m.currentSession != nil {
		info.SessionID = m.currentSession.ID
		info.SessionTitle = m.currentSession.Title
		info.SessionStarted = m.currentSession.CreatedAt.Format(time.RFC3339)
	}
	if m.app.Hooks != nil {
		count := 0
		for _, hs := range m.app.Hooks.ListAll() {
			count += len(hs)
		}
		info.HookCount = count
	}
	if m.app.Costs != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if budget, err := m.app.Costs.CheckBudget(ctx, ""); err == nil {
			info.BudgetDailyMax = budget.DailyLimit
			info.BudgetDailyUsed = budget.DailyUsed
		}
		if m.currentSession != nil {
			if sum, err := m.app.Costs.SessionCost(ctx, m.currentSession.ID); err == nil {
				info.TotalCost = sum.TotalUSD
			}
		}
	}
	if m.app.Agent != nil {
		info.Tools = m.collectToolNames()
	}
	return info
}

// collectToolNames returns the registered tool names for the /status panel.
// The agent doesn't currently expose its registry, so we ask it for a name
// list via reflection-free hook on the agent (ToolNames if present).
func (m *appModel) collectToolNames() []string {
	if m.app == nil || m.app.Agent == nil {
		return nil
	}
	type namer interface{ ToolNames() []string }
	if n, ok := any(m.app.Agent).(namer); ok {
		return n.ToolNames()
	}
	return nil
}

// toastCmd returns a tea.Cmd that emits a ToastMsg of the given kind.
func (m *appModel) toastCmd(text, kind string) tea.Cmd {
	return func() tea.Msg {
		return tuitypes.ToastMsg{Text: text, Kind: kind}
	}
}

// routeDialogKey forwards a key to the currently open dialog and closes it on esc.
func (m *appModel) routeDialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		switch m.openDialog {
		case dialogCommands:
			c, cmd := m.cmdDialog.Update(msg)
			m.cmdDialog = c
			m.openDialog = dialogNone
			return m, cmd
		case dialogModels:
			d, cmd := m.modelDialog.Update(msg)
			m.modelDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogSessions:
			d, cmd := m.sessionDialog.Update(msg)
			m.sessionDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogTheme:
			d, cmd := m.themeDialog.Update(msg)
			m.themeDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogConfig:
			d, cmd := m.setupDialog.Update(msg)
			m.setupDialog = d
			if !d.Show {
				m.openDialog = dialogNone
			}
			return m, cmd
		case dialogHelp:
			h, cmd := m.helpDialog.Update(msg)
			m.helpDialog = *h
			m.openDialog = dialogNone
			return m, cmd
		case dialogStatus:
			d, cmd := m.statusDialog.Update(msg)
			m.statusDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogPermission:
			d, cmd := m.permissionDialog.Update(msg)
			m.permissionDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogTimeline:
			d, cmd := m.timelineDialog.Update(msg)
			m.timelineDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogRename:
			d, cmd := m.renameDialog.Update(msg)
			m.renameDialog = d
			m.openDialog = dialogSessions
			return m, cmd
		case dialogStash:
			d, cmd := m.stashDialog.Update(msg)
			m.stashDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogFileMention:
			d, cmd := m.fileMentionDlg.Update(msg)
			m.fileMentionDlg = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogQuestion:
			d, cmd := m.questionDialog.Update(msg)
			m.questionDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogDiff:
			d, cmd := m.diffDialog.Update(msg)
			m.diffDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogMCP:
			d, cmd := m.mcpDialog.Update(msg)
			m.mcpDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogBrowser:
			d, cmd := m.browserDialog.Update(msg)
			m.browserDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogWorktree:
			d, cmd := m.worktreeDialog.Update(msg)
			m.worktreeDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogPermissionsLedger:
			d, cmd := m.permLedgerDlg.Update(msg)
			m.permLedgerDlg = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogTag:
			d, cmd := m.tagDialog.Update(msg)
			m.tagDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogVariant:
			d, cmd := m.variantDialog.Update(msg)
			m.variantDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogWorkspace:
			d, cmd := m.workspaceDialog.Update(msg)
			m.workspaceDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogSubagentFull:
			d, cmd := m.subagentFullDlg.Update(msg)
			m.subagentFullDlg = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogSkill:
			d, cmd := m.skillDialog.Update(msg)
			m.skillDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogProviderSetup:
			d, cmd := m.provSetupDialog.Update(msg)
			m.provSetupDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogOverrideConfirm:
			d, cmd := m.overrideDialog.Update(msg)
			m.overrideDialog = d
			m.openDialog = dialogNone
			return m, cmd
		case dialogPlugins:
			d, cmd := m.pluginsDialog.Update(msg)
			m.pluginsDialog = d
			m.openDialog = dialogNone
			return m, cmd
		}
	}

	switch m.openDialog {
	case dialogCommands:
		c, cmd := m.cmdDialog.Update(msg)
		m.cmdDialog = c
		return m, cmd
	case dialogModels:
		d, cmd := m.modelDialog.Update(msg)
		m.modelDialog = d
		return m, cmd
	case dialogSessions:
		d, cmd := m.sessionDialog.Update(msg)
		m.sessionDialog = d
		return m, cmd
	case dialogTheme:
		d, cmd := m.themeDialog.Update(msg)
		m.themeDialog = d
		return m, cmd
	case dialogConfig:
		d, cmd := m.setupDialog.Update(msg)
		m.setupDialog = d
		return m, cmd
	case dialogHelp:
		h, cmd := m.helpDialog.Update(msg)
		m.helpDialog = *h
		return m, cmd
	case dialogStatus:
		d, cmd := m.statusDialog.Update(msg)
		m.statusDialog = d
		return m, cmd
	case dialogPermission:
		d, cmd := m.permissionDialog.Update(msg)
		m.permissionDialog = d
		return m, cmd
	case dialogTimeline:
		d, cmd := m.timelineDialog.Update(msg)
		m.timelineDialog = d
		return m, cmd
	case dialogRename:
		d, cmd := m.renameDialog.Update(msg)
		m.renameDialog = d
		return m, cmd
	case dialogStash:
		d, cmd := m.stashDialog.Update(msg)
		m.stashDialog = d
		return m, cmd
	case dialogFileMention:
		d, cmd := m.fileMentionDlg.Update(msg)
		m.fileMentionDlg = d
		// As the user types/backspaces, also reflect editor changes so the
		// trailing @<query> can be sliced out on selection. The dialog owns
		// its own query buffer; we just leave the editor alone here.
		return m, cmd
	case dialogQuestion:
		d, cmd := m.questionDialog.Update(msg)
		m.questionDialog = d
		return m, cmd
	case dialogDiff:
		d, cmd := m.diffDialog.Update(msg)
		m.diffDialog = d
		return m, cmd
	case dialogMCP:
		d, cmd := m.mcpDialog.Update(msg)
		m.mcpDialog = d
		return m, cmd
	case dialogBrowser:
		d, cmd := m.browserDialog.Update(msg)
		m.browserDialog = d
		return m, cmd
	case dialogWorktree:
		d, cmd := m.worktreeDialog.Update(msg)
		m.worktreeDialog = d
		return m, cmd
	case dialogPermissionsLedger:
		d, cmd := m.permLedgerDlg.Update(msg)
		m.permLedgerDlg = d
		return m, cmd
	case dialogTag:
		d, cmd := m.tagDialog.Update(msg)
		m.tagDialog = d
		return m, cmd
	case dialogVariant:
		d, cmd := m.variantDialog.Update(msg)
		m.variantDialog = d
		return m, cmd
	case dialogWorkspace:
		d, cmd := m.workspaceDialog.Update(msg)
		m.workspaceDialog = d
		return m, cmd
	case dialogSubagentFull:
		d, cmd := m.subagentFullDlg.Update(msg)
		m.subagentFullDlg = d
		return m, cmd
	case dialogSkill:
		d, cmd := m.skillDialog.Update(msg)
		m.skillDialog = d
		return m, cmd
	case dialogProviderSetup:
		d, cmd := m.provSetupDialog.Update(msg)
		m.provSetupDialog = d
		return m, cmd
	case dialogOverrideConfirm:
		d, cmd := m.overrideDialog.Update(msg)
		m.overrideDialog = d
		return m, cmd
	case dialogPlugins:
		d, cmd := m.pluginsDialog.Update(msg)
		m.pluginsDialog = d
		return m, cmd
	}
	return m, nil
}

// slashAliases maps casual user inputs to canonical command IDs. Lets users
// type /models or /session without thinking about the canonical singular vs
// plural form.
var slashAliases = map[string]string{
	"models":   "model",
	"session":  "sessions",
	"themes":   "theme",
	"configs":  "config",
	"compacts": "compact",
	"inits":    "init",
	"forks":    "fork",
	"helps":    "help",
	"exit":     "quit",
	"q":        "quit",
}

// resolveSlashCommand maps a user-typed name to a registered command ID.
// Tries exact match → alias → unique prefix match. Returns ("", false) if
// the input is unknown or matches multiple commands ambiguously.
func (m *appModel) resolveSlashCommand(name string) (string, bool) {
	name = strings.ToLower(name)
	if name == "" {
		return "", false
	}
	known := make([]string, 0, len(m.cmdDialog.Commands))
	for _, c := range m.cmdDialog.Commands {
		known = append(known, c.ID)
	}
	for _, id := range known {
		if id == name {
			return id, true
		}
	}
	if id, ok := slashAliases[name]; ok {
		return id, true
	}
	var prefixHits []string
	for _, id := range known {
		if strings.HasPrefix(id, name) {
			prefixHits = append(prefixHits, id)
		}
	}
	if len(prefixHits) == 1 {
		return prefixHits[0], true
	}
	return "", false
}

// dispatchCommandWithArgs handles a /command typed in the editor that may
// carry trailing arguments (e.g. `/stash list`, `/diff path`).
func (m *appModel) dispatchCommandWithArgs(id, args string) tea.Cmd {
	switch id {
	case "stash":
		return m.runStashWith(args)
	case "diff":
		return m.runDiffWith(args)
	case "worktree":
		return m.runWorktreeWith(args)
	case "variant":
		return m.runVariantWith(args)
	case "view":
		return m.runViewWith(args)
	}
	return m.dispatchCommand(id)
}

// runVariantWith parses `/variant <m1> <m2> ...` and runs the variants
// against the current draft (editor contents).
func (m *appModel) runVariantWith(args string) tea.Cmd {
	args = strings.TrimSpace(args)
	if args == "" {
		return m.openVariantPickerHint()
	}
	models := strings.Fields(args)
	if m.app == nil || m.app.Agent == nil {
		return m.toastCmd("variant: no agent", "warning")
	}
	prompt := ""
	if editor := m.chatPage.Editor(); editor != nil {
		prompt = strings.TrimSpace(editor.Value())
	}
	if prompt == "" {
		return m.toastCmd("variant: type a prompt then run /variant", "info")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		results, err := m.app.Agent.RunVariants(ctx, prompt, models)
		if err != nil {
			return tuitypes.ToastMsg{Text: "variant failed: " + err.Error(), Kind: "error"}
		}
		return variantResultsMsg{Results: results}
	}
}

// variantResultsMsg ferries RunVariants output back into the TUI loop so the
// dialog can be opened from the UI goroutine.
type variantResultsMsg struct {
	Results []agent.VariantResult
}

// runViewWith opens the file viewer split-pane for the given path.
func (m *appModel) runViewWith(args string) tea.Cmd {
	path := strings.TrimSpace(args)
	if path == "" {
		return m.toastCmd("usage: /view <path>", "info")
	}
	if m.splitView == nil {
		m.splitView = viewer.NewFileView("")
	}
	if err := m.splitView.Open(path); err != nil {
		return m.toastCmd("view failed: "+err.Error(), "error")
	}
	m.splitOpen = true
	m.splitFocused = false
	// Trigger a resize so the chat shrinks and the viewer gets dimensions.
	if m.width > 0 && m.height > 0 {
		return func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height + 1} }
	}
	return m.toastCmd("opened "+path, "success")
}

// runWorktreeWith implements `/worktree`, `/worktree add <path> [branch]`,
// and `/worktree remove <path>`.
func (m *appModel) runWorktreeWith(args string) tea.Cmd {
	args = strings.TrimSpace(args)
	if args == "" {
		return m.openWorktreeDialog("")
	}
	fields := strings.Fields(args)
	cwd, _ := os.Getwd()
	switch fields[0] {
	case "add":
		if len(fields) < 2 {
			return m.toastCmd("usage: /worktree add <path> [branch]", "info")
		}
		branch := ""
		if len(fields) >= 3 {
			branch = fields[2]
		}
		if err := wt.Add(cwd, fields[1], branch); err != nil {
			return m.toastCmd("worktree add failed: "+err.Error(), "error")
		}
		return m.openWorktreeDialog("created " + fields[1])
	case "remove", "rm":
		if len(fields) < 2 {
			return m.toastCmd("usage: /worktree remove <path>", "info")
		}
		if err := wt.Remove(cwd, fields[1]); err != nil {
			return m.toastCmd("worktree remove failed: "+err.Error(), "error")
		}
		return m.openWorktreeDialog("removed " + fields[1])
	}
	return m.openWorktreeDialog("")
}

// dispatchCommand handles a /command selection from the palette.
func (m *appModel) dispatchCommand(id string) tea.Cmd {
	m.openDialog = dialogNone
	m.cmdDialog.Show = false
	switch id {
	case "help":
		m.refreshHelpDynamic()
		m.openDialog = dialogHelp
		m.helpDialog.Show = true
		return nil
	case "clear":
		m.chatPage.ClearHistory()
		return m.toastCmd("history cleared", "info")
	case "quit":
		return tea.Quit
	case "model":
		return m.openModelDialog()
	case "sessions":
		m.refreshSessionList()
		m.openDialog = dialogSessions
		m.sessionDialog.Show = true
		return nil
	case "theme":
		m.openDialog = dialogTheme
		m.themeDialog, _ = m.themeDialog.Update(dialog.ShowThemeDialogMsg{})
		return nil
	case "config":
		m.openDialog = dialogConfig
		m.setupDialog, _ = m.setupDialog.Update(dialog.ShowSetupDialogMsg{})
		return nil
	case "compact":
		return m.runCompact()
	case "init":
		return m.runInit()
	case "status":
		m.statusDialog.SetInfo(m.collectStatusInfo())
		m.statusDialog.Show = true
		m.openDialog = dialogStatus
		return nil
	case "fork":
		if m.app != nil && m.app.Agent != nil {
			m.timelineDialog.SetMessages(m.app.Agent.History())
			m.openDialog = dialogTimeline
		}
		return nil
	case "stash":
		return m.runStash()
	case "diff":
		return m.runDiff()
	case "mcp":
		return m.openMCPDialog()
	case "browser":
		return m.openBrowserDialog()
	case "plugins":
		return m.openPluginsDialog()
	case "worktree":
		return m.openWorktreeDialog("")
	case "permissions":
		return m.openPermissionsLedger()
	case "tags":
		return m.openTagDialog()
	case "variant":
		return m.openVariantPickerHint()
	case "workspace":
		return m.openWorkspaceDialog()
	case "subagents":
		return m.openSubagentFull()
	case "skills":
		return m.openSkillDialog()
	case "view":
		return m.toastCmd("usage: /view <path>", "info")
	case "sync":
		return m.runSync()
	case "share":
		return m.runShare()
	case "acp":
		return m.runACPStatus()
	case "walls":
		return m.runWalls()
	case "routine":
		return m.runRoutines()
	case "cron":
		return m.runCron()
	case "introspect":
		return m.runIntrospect()
	case "slice":
		return m.runSlice()
	case "diagnose":
		return m.runDiagnose()
	case "plan":
		return m.runPlan()
	case "journal":
		return m.runJournal()
	case "redteam":
		return m.runRedteam()
	case "rollback":
		return m.runRollback()
	case "orders":
		return m.runOrders()
	case "mode":
		return m.runMode()
	case "conceal":
		return m.runConceal()
	case "usage":
		return m.runUsage()
	}
	return nil
}

// runSync pushes the current session to the configured backend, then reports
// status as a toast. Pull is exposed via `overkill sync pull` on the CLI.
func (m *appModel) runSync() tea.Cmd {
	if m.app == nil || m.app.Sync == nil {
		return m.toastCmd("sync: backend not configured", "warning")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		st, err := m.app.Sync.Status(ctx)
		if err != nil {
			return tuitypes.ToastMsg{Text: "sync status: " + err.Error(), Kind: "error"}
		}
		// Push current session in background.
		if m.app.Agent != nil {
			id := m.app.Agent.SessionID()
			if id != "" {
				if err := m.app.Sync.PushOne(ctx, id); err != nil {
					return tuitypes.ToastMsg{Text: "sync push: " + err.Error(), Kind: "error"}
				}
			}
		}
		return tuitypes.ToastMsg{Text: fmt.Sprintf("sync: %s · local %d · remote %d", st.Backend, st.Local, st.Remote), Kind: "success"}
	}
}

// runShare renders the active session as HTML and uploads it.
func (m *appModel) runShare() tea.Cmd {
	if m.app == nil || m.app.Agent == nil || m.app.Store == nil || m.app.Config == nil {
		return m.toastCmd("share: no active session", "warning")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		id := m.app.Agent.SessionID()
		if id == "" {
			return tuitypes.ToastMsg{Text: "share: no session id", Kind: "warning"}
		}
		sess, err := m.app.Store.Load(ctx, id)
		if err != nil {
			return tuitypes.ToastMsg{Text: "share: load: " + err.Error(), Kind: "error"}
		}
		// Hydrate session messages from the live agent so a not-yet-saved
		// conversation is also shareable.
		sess.Messages = m.app.Agent.History()
		html, err := share.Render(sess)
		if err != nil {
			return tuitypes.ToastMsg{Text: "share: render: " + err.Error(), Kind: "error"}
		}
		up, err := share.NewUploader(m.app.Config.Share)
		if err != nil {
			return tuitypes.ToastMsg{Text: "share: " + err.Error(), Kind: "error"}
		}
		url, err := up.Upload(ctx, html)
		if err != nil {
			return tuitypes.ToastMsg{Text: "share: upload: " + err.Error(), Kind: "error"}
		}
		// Best-effort OSC52 copy to clipboard.
		writeOSC52(url)
		return tuitypes.ToastMsg{Text: "shared: " + url, Kind: "success"}
	}
}

// runACPStatus shows ACP server status.
func (m *appModel) runACPStatus() tea.Cmd {
	if m.app == nil || m.app.ACPServer == nil {
		return m.toastCmd("acp: server not enabled (set acp.enabled = true)", "info")
	}
	addr := m.app.ACPServer.Addr()
	tk := m.app.ACPServer.Token()
	writeOSC52(tk)
	return m.toastCmd(fmt.Sprintf("acp: %s · token copied to clipboard", addr), "success")
}

// maybeAutoPushSync fires a non-blocking push of the current session if
// sync is configured AND auto_push is enabled. Delegates to the shared
// helper so the CLI REPL uses the same logic.
func (m *appModel) maybeAutoPushSync() {
	if m.app == nil || m.app.Agent == nil {
		return
	}
	syncpkg.AutoPushIfEnabled(m.app.Config, m.app.Sync, m.app.Agent.SessionID(), nil)
}

// writeOSC52 emits an OSC52 sequence to stdout that asks the terminal to
// copy `s` to the system clipboard. Best-effort — ignored by terminals
// without OSC52 support.
func writeOSC52(s string) {
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", enc)
}

// openPermissionsLedger snapshots the agent ledger and shows it.
func (m *appModel) openPermissionsLedger() tea.Cmd {
	if m.app == nil || m.app.Agent == nil {
		return m.toastCmd("permissions: no agent", "warning")
	}
	entries := m.app.Agent.PermissionLog()
	m.permLedgerDlg.SetEntries(entries)
	m.permLedgerDlg.Show = true
	m.openDialog = dialogPermissionsLedger
	return nil
}

// openTagDialog populates and opens the tag picker.
func (m *appModel) openTagDialog() tea.Cmd {
	if m.app == nil || m.app.Tags == nil {
		return m.toastCmd("tags: not configured", "warning")
	}
	m.tagDialog.SetTags(m.app.Tags.List())
	m.tagDialog.Show = true
	m.openDialog = dialogTag
	return nil
}

// openVariantPickerHint nudges the user toward the args form.
func (m *appModel) openVariantPickerHint() tea.Cmd {
	return m.toastCmd("usage: /variant <model1> <model2> [...]", "info")
}

// openWorkspaceDialog opens the workspace switcher.
func (m *appModel) openWorkspaceDialog() tea.Cmd {
	if m.app == nil || m.app.Workspace == nil {
		return m.toastCmd("workspace: not configured", "warning")
	}
	m.workspaceDialog.SetWorkspaces(m.app.Workspace.List())
	m.workspaceDialog.Show = true
	m.openDialog = dialogWorkspace
	return nil
}

// openSubagentFull populates and shows the subagent detail dialog.
func (m *appModel) openSubagentFull() tea.Cmd {
	if m.app == nil || m.app.Subagent == nil {
		return m.toastCmd("subagents: none", "info")
	}
	children := m.app.Subagent.ActiveChildren()
	m.subagentFullDlg.SetChildren(children)
	m.subagentFullDlg.Show = true
	m.openDialog = dialogSubagentFull
	return nil
}

// openSkillDialog populates the skill list overlay.
func (m *appModel) openSkillDialog() tea.Cmd {
	if m.app == nil {
		return nil
	}
	m.skillDialog.SetSkills(m.app.Skills)
	m.skillDialog.Show = true
	m.openDialog = dialogSkill
	return nil
}

// applyMCPRescan walks current MCP server tools and registers any not yet
// known to the live tool registry. Refreshes the dialog snapshot.
func (m *appModel) applyMCPRescan() tea.Cmd {
	if m.app == nil || m.app.MCP == nil || m.app.Tools == nil {
		return m.toastCmd("mcp: rescan unavailable", "warning")
	}
	added := m.app.MCP.RescanTools(
		func(name string) bool { return m.app.Tools.Has(name) },
		func(adapter *mcppkg.ToolAdapter) error { return m.app.Tools.Register(adapter) },
	)
	m.mcpDialog.SetData(m.app.MCP.Status(), m.app.MCP.Tools())
	return m.toastCmd(fmt.Sprintf("registered %d new mcp tools", added), "success")
}

// openMCPDialog snapshots the MCP manager state and opens the dialog.
// Also performs a one-shot rescan so any tools that handshook after startup
// land in the registry without an explicit user action.
func (m *appModel) openMCPDialog() tea.Cmd {
	if m.app == nil || m.app.MCP == nil {
		return m.toastCmd("mcp: not configured", "info")
	}
	if m.app.Tools != nil {
		m.app.MCP.RescanTools(
			func(name string) bool { return m.app.Tools.Has(name) },
			func(adapter *mcppkg.ToolAdapter) error { return m.app.Tools.Register(adapter) },
		)
	}
	m.mcpDialog.SetData(m.app.MCP.Status(), m.app.MCP.Tools())
	m.mcpDialog.Show = true
	m.openDialog = dialogMCP
	return nil
}

// openBrowserDialog snapshots the browser manager state and opens the dialog.
func (m *appModel) openBrowserDialog() tea.Cmd {
	if m.app == nil || m.app.Browser == nil {
		return m.toastCmd("browser: not enabled (set [browser] enabled = true)", "info")
	}
	m.browserDialog.SetStatus(m.app.Browser.Status())
	m.browserDialog.Show = true
	m.openDialog = dialogBrowser
	return nil
}

// openPluginsDialog opens the full plugins listing modal seeded with the
// current Status() snapshot.
func (m *appModel) openPluginsDialog() tea.Cmd {
	if m.app == nil || m.app.Plugins == nil {
		return m.toastCmd("plugins: not configured", "info")
	}
	m.pluginsDialog.SetData(m.app.Plugins.Status())
	m.pluginsDialog.Show = true
	m.openDialog = dialogPlugins
	return nil
}

// applyPluginToggle flips the disabled state for the named plugin in the
// in-memory cfg and persists the result. The plugin process is not started/
// stopped here — that happens on next launch (matches `disabled` semantics).
func (m *appModel) applyPluginToggle(name string) tea.Cmd {
	if m.app == nil || m.app.Config == nil {
		return nil
	}
	cfg := m.app.Config
	disabled := cfg.Plugins.Disabled
	idx := -1
	for i, n := range disabled {
		if n == name {
			idx = i
			break
		}
	}
	verb := "disabled"
	if idx >= 0 {
		// currently disabled → enable.
		disabled = append(disabled[:idx], disabled[idx+1:]...)
		verb = "enabled"
	} else {
		disabled = append(disabled, name)
	}
	cfg.Plugins.Disabled = disabled
	if m.app.ConfigPath != "" {
		if err := cfg.Save(m.app.ConfigPath); err != nil {
			return m.toastCmd("plugin "+name+": save failed: "+err.Error(), "error")
		}
	}
	// Refresh the dialog snapshot so the row reflects the new state immediately
	// (LastError stays from runtime).
	if m.app.Plugins != nil {
		m.pluginsDialog.SetData(m.app.Plugins.Status())
	}
	return m.toastCmd("plugin "+name+" "+verb+" (restart to apply)", "success")
}

// openPluginsToast surfaces plugin runtime status as a toast. The full
// dialog (pkg/tui/components/dialog/plugins.go) is wired by the host
// integration shipped here; for the slash-command palette we keep the
// quick-look toast.
func (m *appModel) openPluginsToast() tea.Cmd {
	if m.app == nil || m.app.Plugins == nil {
		return m.toastCmd("plugins: not configured", "info")
	}
	statuses := m.app.Plugins.Status()
	if len(statuses) == 0 {
		return m.toastCmd("plugins: none installed (~/.overkill/plugins/)", "info")
	}
	parts := make([]string, 0, len(statuses))
	for _, s := range statuses {
		state := "ok"
		if !s.Running {
			state = "down"
			if s.LastError != "" {
				state = "err"
			}
		}
		parts = append(parts, fmt.Sprintf("%s (%s · %dt %dc)", s.Name, state, s.Tools, s.Commands))
	}
	return m.toastCmd("plugins: "+strings.Join(parts, ", "), "info")
}

// openWorktreeDialog lists worktrees from the current cwd and opens the dialog.
func (m *appModel) openWorktreeDialog(hint string) tea.Cmd {
	cwd, _ := os.Getwd()
	wts, err := worktreeList(cwd)
	if err != nil {
		return m.toastCmd("worktree: "+err.Error(), "warning")
	}
	m.worktreeDialog.SetEntries(wts)
	m.worktreeDialog.SetHint(hint)
	m.worktreeDialog.Show = true
	m.openDialog = dialogWorktree
	return nil
}

// runStash dispatched from the command palette. Saves whatever's currently in
// the editor (with no args path), or shows the list when called bare.
func (m *appModel) runStash() tea.Cmd {
	return m.runStashWith("")
}

// runStashWith implements `/stash` and `/stash list`.
func (m *appModel) runStashWith(args string) tea.Cmd {
	args = strings.TrimSpace(args)
	if args == "list" {
		return m.openStashList()
	}
	if m.stashStore == nil {
		return m.toastCmd("stash: storage unavailable", "warning")
	}
	// If args were provided, save those; otherwise fall back to whatever the
	// user has in the editor right now.
	text := args
	if text == "" {
		if editor := m.chatPage.Editor(); editor != nil {
			text = strings.TrimSpace(editor.Value())
		}
	}
	if text == "" {
		return m.openStashList()
	}
	if _, err := m.stashStore.Save(text); err != nil {
		return m.toastCmd("stash failed: "+err.Error(), "error")
	}
	if editor := m.chatPage.Editor(); editor != nil {
		editor.SetValue("")
	}
	return m.toastCmd(fmt.Sprintf("stashed %d chars", len(text)), "success")
}

// openStashList loads stash entries and opens the dialog.
func (m *appModel) openStashList() tea.Cmd {
	if m.stashStore == nil {
		return m.toastCmd("stash: storage unavailable", "warning")
	}
	entries, err := m.stashStore.List()
	if err != nil {
		return m.toastCmd("stash list failed: "+err.Error(), "error")
	}
	m.stashDialog.SetEntries(entries)
	m.stashDialog.Show = true
	m.openDialog = dialogStash
	return nil
}

// runDiff dispatched from the palette without arguments — show usage hint.
func (m *appModel) runDiff() tea.Cmd {
	return m.runDiffWith("")
}

// runDiffWith renders a unified diff for the given path via `git diff`.
// Supports a trailing `--split` flag to open directly in side-by-side mode.
func (m *appModel) runDiffWith(args string) tea.Cmd {
	args = strings.TrimSpace(args)
	if args == "" {
		return m.toastCmd("usage: /diff <path> [--split]", "info")
	}
	split := false
	fields := strings.Fields(args)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "--split" || f == "-s" {
			split = true
			continue
		}
		out = append(out, f)
	}
	path := strings.Join(out, " ")
	if path == "" {
		return m.toastCmd("usage: /diff <path> [--split]", "info")
	}
	cwd, _ := os.Getwd()
	diffOut, err := runGit(cwd, "diff", "--", path)
	if err != nil || strings.TrimSpace(diffOut) == "" {
		return m.toastCmd("no diff for "+path, "info")
	}
	m.diffDialog.SetDiff(path, diffOut)
	m.diffDialog.SetSplitMode(split)
	m.diffDialog.Show = true
	m.openDialog = dialogDiff
	return nil
}

// runCompact invokes the agent's compaction API and toasts the result.
func (m *appModel) runCompact() tea.Cmd {
	if m.app == nil || m.app.Agent == nil {
		return m.toastCmd("compact: no agent", "warning")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := m.app.Agent.Compact(ctx)
		if err != nil {
			return tuitypes.ToastMsg{Text: "compact failed: " + err.Error(), Kind: "error"}
		}
		return tuitypes.ToastMsg{
			Text: fmt.Sprintf("compacted: %d → %d tokens", res.TokensBefore, res.TokensAfter),
			Kind: "success",
		}
	}
}

// runInit writes a starter .overkill/ directory in the current cwd.
func (m *appModel) runInit() tea.Cmd {
	cwd, err := os.Getwd()
	if err != nil {
		return m.toastCmd("init failed: cwd", "error")
	}
	dir := filepath.Join(cwd, ".overkill")
	if _, err := os.Stat(dir); err == nil {
		return m.toastCmd("already exists", "warning")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return m.toastCmd("init failed: mkdir", "error")
	}
	provider := ""
	model := ""
	if m.app != nil && m.app.Config != nil {
		provider = m.app.Config.Agent.DefaultProvider
		model = m.app.Config.Agent.DefaultModel
	}
	cfgBody := "# overkill project config (generated by /init)\n" +
		"[agent]\n" +
		"name = \"overkill\"\n" +
		"default_provider = \"" + provider + "\"\n" +
		"default_model = \"" + model + "\"\n"
	_ = os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfgBody), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "system_prompt.md"), []byte("# project system prompt\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "AGENT.md"), []byte("# project notes for overkill\n"), 0o644)

	// Seed the deep-wiki for the agent: ~/.overkill/introspection/{CODEBASE,PRP}.md.
	// Best-effort — failure to scan or write does not block /init.
	if home, err := os.UserHomeDir(); err == nil {
		introDir := filepath.Join(home, ".overkill", "introspection")
		_, scanErr := introspection.WriteCodebaseFromScan(cwd, introDir)
		_, _ = introspection.WritePRP(introspection.PRPInputs{
			ProjectName: filepath.Base(cwd),
			RepoRoot:    cwd,
			Languages:   introspection.DetectLanguages(cwd),
			OutputDir:   introDir,
		})
		if scanErr != nil {
			return m.toastCmd("initialized .overkill/ + PRP.md (CODEBASE skipped: "+scanErr.Error()+")", "warning")
		}
		return m.toastCmd("initialized .overkill/ + CODEBASE.md + PRP.md", "success")
	}
	return m.toastCmd("initialized .overkill/", "success")
}

func (m *appModel) sendToChat(msg tea.Msg) tea.Cmd {
	updated, cmd := m.chatPage.Update(msg)
	m.chatPage = updated
	return cmd
}

func (m *appModel) View() string {
	// Render rate limit: cap at ~30 fps (33ms between frames) on fast links,
	// ~15 fps (66ms) on settled states. Over SSH this keeps bandwidth
	// manageable without visible stutter.
	//
	// CRITICAL: we must NOT suppress the very first render (lastRenderAt
	// zero) or renders during boot/onboarding/unknown-width — those represent
	// state transitions and must always pass through. The previous 16ms
	// limiter was too aggressive and returned stale content even when it
	// changed, causing perceived hangs on SSH.
	//
	// Dynamic floor: 33ms (30fps) when streaming/busy, 66ms (15fps) when
	// idle so spinner ticks don't cause unnecessary redraws.
	if !m.lastRenderAt.IsZero() && m.lastRenderOut != "" &&
		!m.boot.visible && m.onboarding == nil && m.width > 0 {
		now := time.Now()
		elapsed := now.Sub(m.lastRenderAt)
		floor := 66 * time.Millisecond
		if m.chatPage.IsBusy() {
			floor = 33 * time.Millisecond
		}
		if elapsed < floor {
			return m.lastRenderOut
		}
	}

	var now time.Time // declared here, set before each cache-return path below

	if m.boot.visible {
		m.boot.width = m.width
		m.boot.height = m.height
		out := m.boot.View()
		now = time.Now()
		m.lastRenderAt = now
		m.lastRenderOut = out
		return out
	}

	if m.width <= 0 {
		return "starting overkill..."
	}

	if m.onboarding != nil {
		m.onboarding.SetSize(m.width, m.height)
		out := m.onboarding.View()
		now = time.Now()
		m.lastRenderAt = now
		m.lastRenderOut = out
		return out
	}

	chatView := m.chatPage.View()

	if m.splitOpen && m.splitView != nil && m.width >= 80 {
		// Split-view replaces the sidebar — they don't compose.
		splitView := m.splitView.View()
		chatView = lipgloss.JoinHorizontal(lipgloss.Top, chatView, splitView)
	} else if m.showSidebar && m.width >= 60 && m.chatPage.HasMessages() {
		sidebarView := m.sidebar.View()
		if sidebarView != "" {
			chatView = lipgloss.JoinHorizontal(lipgloss.Top, chatView, sidebarView)
		}
	}

	statusView := m.statusBar.View()
	subagentView := m.subagentFooterView()

	parts := []string{chatView}
	if subagentView != "" {
		parts = append(parts, subagentView)
	}
	parts = append(parts, statusView)
	base := lipgloss.JoinVertical(lipgloss.Top, parts...)

	if t := m.toast.View(); t != "" {
		// Anchor the toast bottom-right so it doesn't fight with the editor.
		base = layout.PlaceOverlay(max0(m.width-lipgloss.Width(t)-2), max0(m.height-3), t, base, true)
	}

	if m.openDialog != dialogNone {
		overlay := m.dialogView()
		if overlay != "" {
			ow := lipgloss.Width(overlay)
			oh := lipgloss.Height(overlay)
			col := (m.width - ow) / 2
			row := (m.height - oh) / 2
			if col < 0 {
				col = 0
			}
			if row < 0 {
				row = 0
			}
			base = layout.PlaceOverlay(col, row, overlay, base, true)
		}
	}

	now = time.Now()
	m.lastRenderAt = now
	m.lastRenderOut = base
	return base
}

// subagentFooterView renders a focusable list of running subagents. Each
// child is its own segment so the user can ↑/↓ between them and Enter to
// open the full detail dialog. When subagentFooterCursor is -1 (default) we
// render every entry as muted with a leading "subagents:" label.
func (m *appModel) subagentFooterView() string {
	if m.app == nil || m.app.Subagent == nil {
		return ""
	}
	children := m.app.Subagent.ActiveChildren()
	if len(children) == 0 {
		return ""
	}
	muted := lipgloss.NewStyle().Faint(true)
	focused := lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("12")).Bold(true)
	now := time.Now()
	parts := make([]string, len(children))
	for i, c := range children {
		elapsed := now.Sub(c.StartedAt).Round(time.Second)
		goal := c.Goal
		if len(goal) > 30 {
			goal = goal[:27] + "..."
		}
		entry := fmt.Sprintf(" %s · %s · %s ", goal, c.Status, elapsed)
		if i == m.subagentFooterCursor {
			parts[i] = focused.Render(entry)
		} else {
			parts[i] = muted.Render(entry)
		}
	}
	return muted.Render("subagents:") + " " + strings.Join(parts, muted.Render(" | "))
}

// subagentChildCount returns the live count of running subagent children.
func (m *appModel) subagentChildCount() int {
	if m.app == nil || m.app.Subagent == nil {
		return 0
	}
	return len(m.app.Subagent.ActiveChildren())
}

// editorEmpty reports whether the current chat editor has no input. Used
// to gate footer cursor activation so we don't steal arrows from typing.
func (m *appModel) editorEmpty() bool {
	editor := m.chatPage.Editor()
	if editor == nil {
		return true
	}
	return strings.TrimSpace(editor.Value()) == ""
}

func (m *appModel) dialogView() string {
	switch m.openDialog {
	case dialogCommands:
		return m.cmdDialog.View(m.width, m.height)
	case dialogModels:
		return m.modelDialog.View(m.width, m.height)
	case dialogSessions:
		return m.sessionDialog.View(m.width, m.height)
	case dialogTheme:
		return m.themeDialog.View(m.width, m.height)
	case dialogConfig:
		return m.setupDialog.View(m.width, m.height)
	case dialogHelp:
		return m.helpDialog.View(m.width, m.height)
	case dialogStatus:
		return m.statusDialog.View(m.width, m.height)
	case dialogPermission:
		return m.permissionDialog.View(m.width, m.height)
	case dialogTimeline:
		return m.timelineDialog.View(m.width, m.height)
	case dialogRename:
		return m.renameDialog.View(m.width, m.height)
	case dialogStash:
		return m.stashDialog.View(m.width, m.height)
	case dialogFileMention:
		return m.fileMentionDlg.View(m.width, m.height)
	case dialogQuestion:
		return m.questionDialog.View(m.width, m.height)
	case dialogDiff:
		return m.diffDialog.View(m.width, m.height)
	case dialogMCP:
		return m.mcpDialog.View(m.width, m.height)
	case dialogBrowser:
		return m.browserDialog.View(m.width, m.height)
	case dialogWorktree:
		return m.worktreeDialog.View(m.width, m.height)
	case dialogPermissionsLedger:
		return m.permLedgerDlg.View(m.width, m.height)
	case dialogTag:
		return m.tagDialog.View(m.width, m.height)
	case dialogVariant:
		return m.variantDialog.View(m.width, m.height)
	case dialogWorkspace:
		return m.workspaceDialog.View(m.width, m.height)
	case dialogSubagentFull:
		return m.subagentFullDlg.View(m.width, m.height)
	case dialogSkill:
		return m.skillDialog.View(m.width, m.height)
	case dialogProviderSetup:
		return m.provSetupDialog.View(m.width, m.height)
	case dialogOverrideConfirm:
		return m.overrideDialog.View(m.width, m.height)
	case dialogPlugins:
		return m.pluginsDialog.View(m.width, m.height)
	case dialogDeleteConfirm:
		title := "(unknown)"
		if m.deleteSession != nil {
			title = m.deleteSession.Title
		}
		body := "delete session '" + title + "'?\n\n[y] yes   [n] no"
		d := dialog.Dialog{Title: "confirm delete"}
		return d.BaseView(body, m.width, m.height)
	}
	return ""
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
