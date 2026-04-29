# Ethos TUI Design — Phase 2

> Vertical-slice approach: 4 waves, test-first, 3 sub-agents per wave.
> Ship working chat in wave 1, personality as capstone in wave 4.

---

## 1. Architecture & Package Layout

```
pkg/tui/
├── tui.go                    # Root app model, Init/Update/View, key bindings
├── keys.go                   # Key map definitions (Ctrl+O/S/K/?, Ctrl+C, etc.)
├── types.go                  # Shared message types, Focusable/Sizeable/Bindings interfaces
├── app.go                    # App wiring struct (holds all internal/* instances)
├── theme/
│   ├── theme.go              # Theme interface + 40 color slots + CurrentTheme()
│   └── catppuccin.go         # Built-in Catppuccin Mocha theme
├── styles/
│   ├── styles.go             # Base styles, borders, spacing helpers
│   └── markdown.go           # Glamour renderer wrapper with theme integration
├── page/
│   ├── page.go               # PageID enum, PageChangeMsg
│   └── chat.go               # Chat page: messages + editor + split-pane assembly
├── components/
│   ├── chat/
│   │   ├── message.go        # Single message rendering (user/assistant/tool)
│   │   ├── messagelist.go    # Scrollable message list with render caching
│   │   └── editor.go         # Input textarea with paste + Ctrl+E $EDITOR support
│   ├── sidebar/
│   │   ├── sidebar.go        # Sidebar container, tab switching between panels
│   │   ├── cost.go           # Token/cost stats panel (reuses internal/cost)
│   │   ├── files.go          # Modified files panel with diff stats
│   │   └── session.go        # Session info panel
│   ├── status/
│   │   └── statusbar.go      # Bottom bar: model, personality mode, tokens, cost
│   └── dialog/
│       ├── dialog.go         # Base dialog: centered overlay, border, close on Esc
│       ├── help.go           # Help overlay (Ctrl+?)
│       ├── models.go         # Model picker (Ctrl+O): vertical list, provider tabs
│       ├── session.go        # Session switcher (Ctrl+S)
│       ├── commands.go       # Command palette (Ctrl+K): fuzzy search
│       └── quit.go           # Quit confirmation
├── layout/
│   ├── split.go              # Horizontal/vertical split helpers
│   └── overlay.go            # PlaceOverlay: center dialog over content
└── util/
    └── util.go               # CmdHandler, InfoMsg, spinner states
```

### Dependencies (added to go.mod)

- `github.com/charmbracelet/bubbletea` — Elm architecture framework
- `github.com/charmbracelet/lipgloss` — Terminal styling
- `github.com/charmbracelet/bubbles` — Pre-built components (textarea, spinner, viewport, list)
- `github.com/charmbracelet/glamour` — Markdown rendering
- `github.com/charmbracelet/x/ansi` — ANSI utilities (truncation)

### Key Interfaces

```go
type Focusable interface {
    Focus() tea.Cmd
    Blur() tea.Cmd
    IsFocused() bool
}

type Sizeable interface {
    SetSize(width, height int) tea.Cmd
    GetSize() (int, int)
}

type Bindings interface {
    BindingKeys() []key.Binding
}
```

### Root Model Pattern

Root `appModel` follows OpenCode's proven pattern: holds pages map, overlay show/hide bools, status bar, wires key events to dialogs. Messages flow down: `appModel` → page → components.

---

## 2. Data Flow & Integration

### App Wiring Struct

```go
// pkg/tui/app.go
type App struct {
    Agent   *agent.Agent
    Store   session.Store
    Router  *routing.SmartRouter
    Costs   *cost.Tracker
    Hooks   *hooks.Registry
    Config  *config.Config
    Person  *personality.Engine
    Journal *journal.Recorder
    Cron    *cron.Scheduler
}
```

Constructed in `cmd/ethos/run.go`, passed to `tui.New(app)`.

### Custom Message Types (types.go)

```go
type SendMsg struct {
    Text        string
    Attachments []Attachment
}

type AgentResponseMsg struct {
    Content string
    Done    bool
    Err     error
}

type AgentStreamMsg struct {
    Chunk  string
    Tokens int
}

type SessionLoadedMsg struct {
    Session *session.Session
}

type CostUpdateMsg struct {
    TotalCost    float64
    InputTokens  int64
    OutputTokens int64
    ContextPct   float64
}

type FilesChangedMsg struct {
    Files []FileChange
}

type FileChange struct {
    Path    string
    Added   int
    Deleted int
    Status  string // modified, added, deleted
}

type PersonalityStateMsg struct {
    Mode          personality.Mode
    Relationship  int
    FunFact       string
    SoulMDExcerpt string
}

type BootCompleteMsg struct {
    FunFact string
    SoulMD  string
}

type BridgeStatusMsg struct {
    Connected bool
    Err       error
}

type SessionListMsg struct {
    Sessions []*session.Session
}

type ModelSelectedMsg struct {
    ModelID string
}

type Attachment struct {
    Name    string
    Content []byte
    Type    string // "image", "file"
}
```

### Prerequisites

The following methods must be added to existing `internal/` packages before TUI integration:

| Package | Method | Purpose |
|---------|--------|---------|
| `internal/agent` | `SetModel(id string) error` | Switch model at runtime |
| `internal/agent` | `IsBusy() bool` | Check if agent is processing |
| `internal/cost` | `GetStatus() CostStatus` | Get current token/cost snapshot |

### Data Flow Diagrams

**User sends message:**
Editor → `SendMsg` → ChatPage calls `agent.Process(ctx, text)` → agent streams `AgentStreamMsg` chunks → message list appends → `Done=true` fires `CostUpdateMsg` from cost tracker

**File changes:**
On each agent turn completion → `git diff --stat` via `internal/tools/git` → `FilesChangedMsg` → sidebar files panel updates

**Model switch:**
Model picker → `ModelSelectedMsg` → root calls `agent.SetModel(id)` → status bar updates

**Session switch:**
Session picker → `SessionSelectedMsg` → root calls `store.Load(ctx, id)` → `SessionLoadedMsg` → ChatPage refreshes

**Boot sequence:**
`Init()` fires async cmd → loads soul.md + picks fun fact + loads relationship → `BootCompleteMsg` → status bar personality indicator + fun fact toast

---

## 3. Wave Breakdown with Test Cases

### Wave 1: Shell + Chat (40 tests, 3 sub-agents)

**Sub-agent A: Theme + Types + Styles + Layout (~12 tests)**

| Test | Verifies |
|------|----------|
| TestTheme_ColorSlots | All 40 slots return non-zero colors |
| TestTheme_CurrentTheme | CurrentTheme() returns non-nil |
| TestTheme_CatppuccinDefaults | Catppuccin Mocha colors are correct |
| TestStyles_BaseStyle | BaseStyle() returns lipgloss.Style with expected padding |
| TestStyles_BorderStyle | Border styles render with rounded corners |
| TestSplit_Horizontal | Horizontal split joins two strings side by side |
| TestSplit_Vertical | Vertical split stacks strings |
| TestSplit_ZeroWidth | Zero-width panel returns empty string |
| TestOverlay_Center | PlaceOverlay centers dialog correctly |
| TestOverlay_OffScreen | Dialog larger than viewport clips gracefully |
| TestTypes_Focusable | Focusable interface methods work |
| TestTypes_Sizeable | Sizeable interface methods work |

**Sub-agent B: Chat Page — Messages + Editor (~16 tests)**

| Test | Verifies |
|------|----------|
| TestChatPage_Init | Init() returns batch of cmds |
| TestChatPage_UpdateWindowSize | Handles tea.WindowSizeMsg, sets width/height |
| TestChatPage_SendMessage | SendMsg triggers agent.Process |
| TestChatPage_ReceiveStream | AgentStreamMsg appends chunk to current message |
| TestChatPage_ReceiveComplete | Done=true finalizes message |
| TestChatPage_ReceiveError | Err shows error message |
| TestChatPage_EditorFocus | Focus/Blur toggles editor focus |
| TestChatPage_ScrollUp | KeyMsg "up" scrolls viewport |
| TestChatPage_ScrollDown | KeyMsg "down" scrolls viewport |
| TestMessage_UserRender | User message renders with correct role label |
| TestMessage_AssistantRender | Assistant message renders with markdown |
| TestMessage_ToolRender | Tool call renders with tool name + result |
| TestMessage_Truncation | Long messages truncate to viewport width |
| TestMessage_CacheKey | Same ID + width = same cache key |
| TestMessage_EmptyContent | Empty content renders placeholder |
| TestMessageList_Append | Appending message updates count and scroll |

**Sub-agent C: Status Bar (~12 tests)**

| Test | Verifies |
|------|----------|
| TestStatusBar_Init | Init() returns spinner cmd |
| TestStatusBar_UpdateSpinner | Tick updates spinner frame |
| TestStatusBar_ViewIdle | Idle: model name + personality mode + "Ready" |
| TestStatusBar_ViewThinking | "Thinking..." with spinner |
| TestStatusBar_ViewGenerating | "Generating..." with spinner |
| TestStatusBar_ViewToolCall | Tool name with spinner |
| TestStatusBar_CostDisplay | Shows "Tokens: 1.2K | Cost: $0.05" |
| TestStatusBar_CostWarning | >80% budget shows warning color |
| TestStatusBar_Personality | Shows personality mode indicator |
| TestStatusBar_SessionName | Shows current session name |
| TestStatusBar_ContextPercent | Shows context window usage % |
| TestStatusBar_Resize | Handles WindowSizeMsg |

### Wave 2: Sidebar + Split Layout (30 tests, 3 sub-agents)

**Sub-agent A: Split Layout + Sidebar Container (~10 tests)**

| Test | Verifies |
|------|----------|
| TestSplitPane_Ratio | 70/30 split calculates correct widths |
| TestSplitPane_MinWidth | Below 60 cols collapses sidebar |
| TestSplitPane_Resize | WindowSizeMsg recalculates split |
| TestSidebar_Init | Init() returns batch |
| TestSidebar_SwitchTab | Tab cycles through panels |
| TestSidebar_ActiveHighlight | Active tab has distinct style |
| TestSidebar_Render | View() renders active panel content |
| TestSidebar_Resize | Sizeable interface works |
| TestSidebar_Focus | Focusable toggles |
| TestSidebar_NoContent | Empty state shows "No data" |

**Sub-agent B: Cost + Session Panels (~12 tests)**

| Test | Verifies |
|------|----------|
| TestCostPanel_Render | Shows input/output/cache tokens + cost |
| TestCostPanel_DailyBudget | Shows daily budget with progress bar |
| TestCostPanel_BudgetWarning | >80% daily budget shows warning |
| TestCostPanel_PerTaskBudget | Shows per-task budget usage |
| TestCostPanel_RollingWindow | Shows 5hr rolling window spend |
| TestCostPanel_Empty | No cost data shows "No usage yet" |
| TestSessionPanel_Render | Shows session ID, name, created, message count |
| TestSessionPanel_ListSessions | Lists recent sessions |
| TestSessionPanel_Empty | No sessions shows "No sessions" |
| TestSessionPanel_Truncate | Long session names truncate |
| TestSessionPanel_FormatTime | Timestamps formatted correctly |
| TestSessionPanel_MessageCount | Shows correct message count |

**Sub-agent C: File Change Panel (~8 tests)**

| Test | Verifies |
|------|----------|
| TestFilePanel_Render | Shows files with +added -deleted stats |
| TestFilePanel_NoChanges | No changes shows "No file changes" |
| TestFilePanel_LongPath | Long paths truncate with ellipsis |
| TestFilePanel_ColorCoding | Added=green, deleted=red, modified=yellow |
| TestFilePanel_SortByChange | Files sorted by magnitude of change |
| TestFilePanel_BinaryFiles | Binary files show "(binary)" not diff stats |
| TestFilePanel_MaxFiles | >20 files shows "+N more" |
| TestFilePanel_Refresh | FilesChangedMsg updates panel |

### Wave 3: Dialogs (35 tests, 3 sub-agents)

**Sub-agent A: Dialog Framework + Quit + Help (~12 tests)**

| Test | Verifies |
|------|----------|
| TestDialog_Base | Base dialog renders centered with border |
| TestDialog_CloseOnEsc | Esc key closes dialog |
| TestDialog_CloseMsg | CloseDialogMsg sets show=false |
| TestDialog_Resize | Dialog adapts to window size |
| TestDialog_BlockKeys | Open dialog blocks key events from page below |
| TestQuit_Show | ShowQuitMsg sets show=true |
| TestQuit_Confirm | "y" sends tea.Quit |
| TestQuit_Cancel | "n"/Esc closes dialog |
| TestQuit_View | Renders "Quit? y/n" |
| TestHelp_Show | ShowHelpMsg sets show=true |
| TestHelp_Bindings | Displays all key bindings |
| TestHelp_Close | Esc closes help |

**Sub-agent B: Model Picker + Session Switcher (~14 tests)**

| Test | Verifies |
|------|----------|
| TestModelPicker_Show | ShowModelDialogMsg sets show=true |
| TestModelPicker_ListModels | Lists all available models from router |
| TestModelPicker_FilterByProvider | Tab switches provider group |
| TestModelPicker_SelectModel | Enter sends ModelSelectedMsg |
| TestModelPicker_FuzzySearch | Typing filters model list (substring match for MVP) |
| TestModelPicker_Highlight | Up/down arrows change selection |
| TestModelPicker_MaxWidth | Model names truncated to 40 chars |
| TestModelPicker_Close | Esc closes |
| TestSessionSwitcher_Show | Ctrl+S opens, loads sessions |
| TestSessionSwitcher_List | Lists sessions sorted by recency |
| TestSessionSwitcher_Select | Enter sends SessionSelectedMsg |
| TestSessionSwitcher_Highlight | Up/down arrows change selection |
| TestSessionSwitcher_Empty | No sessions shows warning |
| TestSessionSwitcher_Close | Esc closes |

**Sub-agent C: Command Palette (~9 tests)**

| Test | Verifies |
|------|----------|
| TestCommandPalette_Show | Ctrl+K opens |
| TestCommandPalette_List | Lists registered commands |
| TestCommandPalette_FuzzyMatch | Typing filters by title/description (substring match for MVP) |
| TestCommandPalette_Select | Enter sends CommandSelectedMsg |
| TestCommandPalette_Highlight | Up/down arrows |
| TestCommandPalette_Empty | No matches shows "No results" |
| TestCommandPalette_Register | RegisterCommand adds to list |
| TestCommandPalette_Handler | CommandSelectedMsg fires handler |
| TestCommandPalette_Close | Esc closes |

### Wave 4: Boot + Personality + Polish (25 tests, 3 sub-agents)

**Sub-agent A: Boot Sequence (~9 tests)**

| Test | Verifies |
|------|----------|
| TestBoot_LoadSoulMD | Boot loads soul.md content |
| TestBoot_LoadFunFact | Boot picks contextual fun fact |
| TestBoot_LoadRelationship | Boot loads relationship state |
| TestBoot_BootComplete | BootCompleteMsg sets state correctly |
| TestBoot_BootView | Shows ASCII art + fun fact + soul.md excerpt |
| TestBoot_BootFade | Boot view fades after first keypress |
| TestBoot_EmptySoulMD | Missing soul.md shows default greeting |
| TestBoot_FirstRun | No sessions shows "hey you're finally awake" |
| TestBoot_PersonalityLoaded | Personality engine initialized from config |

**Sub-agent B: Personality UI (~8 tests)**

| Test | Verifies |
|------|----------|
| TestPersonality_StatusBar | Status bar shows current mode emoji + name |
| TestPersonality_SwitchMode | Mode change updates indicator |
| TestPersonality_FunFactToast | Fun fact appears as toast notification |
| TestPersonality_FunFactTimer | Toast disappears after 5 seconds |
| TestPersonality_Relationship | Relationship level shown in status bar |
| TestPersonality_WittyMessages | Witty mode adds personality to status messages |
| TestPersonality_OffMode | Off mode shows neutral indicator |
| TestPersonality_FullMode | Full mode shows max personality |

**Sub-agent C: Markdown + Caching + Polish (~8 tests)**

| Test | Verifies |
|------|----------|
| TestMarkdown_Render | Glamour renders with theme colors |
| TestMarkdown_CodeBlock | Code blocks get syntax highlighting |
| TestMarkdown_Table | Tables render correctly |
| TestMarkdown_Truncate | Wide content wraps to viewport width |
| TestMessageCache_Hit | Same ID + width returns cached render |
| TestMessageCache_Invalidate | Width change invalidates cache |
| TestMessageCache_Bounded | Cache evicts at 100 messages |
| TestMessageCache_Clear | Clear empties cache |

### Totals

**130 tests, 4 waves, 12 sub-agent dispatches.**

Wave flow: write tests → dispatch 3 sub-agents → `go build ./pkg/tui/... && go test -race ./pkg/tui/...` → fix → next wave.

---

## 4. Error Handling & Edge Cases

### Error Categories

| Source | Error | TUI Behavior |
|--------|-------|--------------|
| Agent | Network/API failure | Red error bubble in message list, status bar "Error: connection failed" |
| Agent | Budget exceeded | Block send, budget warning dialog, status bar flashes red |
| Agent | Context window full | Auto-trigger compaction, "Compacting..." overlay |
| Agent | Tool permission denied | Show in message stream, no crash |
| Store | BadgerDB corrupt | Status bar error, suggest `ethos doctor` |
| Store | Session not found | Clear chat, "Session not found" toast |
| Store | Switch during busy | Block with "Agent is busy" status |
| Bridge | Unavailable on boot | Log warning, disable vector search, keyword-only memory |
| Bridge | Dies mid-session | "Bridge disconnected" status, retry with backoff |

### Terminal Size Edge Cases

| Size | Behavior |
|------|----------|
| < 60 cols | Collapse sidebar, full-width chat only |
| < 20 cols | Show "Terminal too small" centered, no render |
| < 10 rows | Editor hidden, messages only |
| Resize during stream | Recalculate layout, preserve in-flight chunks |

### Startup Edge Cases

| Case | Behavior |
|------|----------|
| No config file | Auto-run `ethos doctor` inline, then setup wizard dialog |
| No API keys | Init dialog: "Configure providers before chatting" |
| Empty session | Show boot splash (ASCII art + fun fact) |
| First run ever | "hey you're finally awake" + setup wizard |

### Runtime Edge Cases

| Case | Behavior |
|------|----------|
| Agent stuck (>60s) | Show "Still thinking..." with elapsed timer |
| Massive message (>10K chars) | Truncate render, "Press Enter to expand" |
| Concurrent key spam | Debounce, no double-send |
| Quit during work | "Agent is running. Quit anyway? y/n" |

### Graceful Shutdown Sequence

1. Stop agent (cancel context)
2. Save session to BadgerDB
3. Record journal entry via `internal/journal`
4. Stop cron scheduler
5. Close bridge connection
6. `tea.Quit`

### Async Operations

All blocking work runs as `tea.Cmd` (goroutine → message). Never block `Update()`.

| Operation | Message Returned |
|-----------|-----------------|
| `agent.Process()` | `AgentStreamMsg` chunks |
| `sessionStore.List()` | `SessionListMsg` |
| `git diff --stat` | `FilesChangedMsg` |
| `costTracker.GetStatus()` | `CostUpdateMsg` |
| `personalityEngine.GetFunFact()` | `PersonalityStateMsg` |
| `bridge.Client.Ping()` | `BridgeStatusMsg` |

---

## 5. CLI Integration

### `cmd/ethos/run.go` Changes

```go
func runCmd(cmd *cobra.Command, args []string) error {
    cfg, err := config.Load(cfgPath)
    // ... existing validation ...

    app := &tui.App{
        Agent:   agent.New(cfg, providers, tools, hooks, security),
        Store:   sessionStore,
        Router:  router,
        Costs:   costTracker,
        Hooks:   hookRegistry,
        Config:  cfg,
        Person:  personalityEngine,
        Journal: journalRecorder,
        Cron:    cronScheduler,
    }

    model := tui.New(app)
    p := tea.NewProgram(model,
        tea.WithAltScreen(),
        tea.WithMouseCellMotion(),
    )
    _, err = p.Run()
    return err
}
```

### CLI Flags for `ethos run`

| Flag | Short | Purpose |
|------|-------|---------|
| `--personality` | `-p` | Override personality mode (off/subtle/witty/full) |
| `--model` | `-m` | Override default model |
| `--session` | `-s` | Resume specific session ID |
| `--no-bridge` | | Skip Python bridge, keyword-only memory |
