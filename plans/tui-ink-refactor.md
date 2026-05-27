# Plan: Bubble Tea → Ink TUI Refactor

## Why

Bubble Tea + BadgerDB is killing the Go value prop. BadgerDB corrupts itself into 2GB vlog files, Bubble Tea's Model-Update-View pattern allocates wildly, and the TUI is tightly coupled to the backend. We want Hermes-level polish (Ink 6 + React 19 + unicode-animations) with a clean API boundary.

## Architecture Target

```
┌──────────────────────────────────┐
│  Ink TUI (TypeScript/React)      │  ← New
│  components/, hooks/, themes/    │
├──────────────────────────────────┤
│  JSON-RPC over stdio/HTTP        │  ← New API layer
├──────────────────────────────────┤
│  Go backend (unchanged)          │
│  agent, tools, providers, etc.   │
│  session → SQLite (not BadgerDB) │  ← Optional: swap storage
└──────────────────────────────────┘
```

The Go backend already has `bridge/` for gRPC to Python. We add a new JSON-RPC server
for the Ink frontend to consume. This is the same pattern Hermes uses (tui_gateway/ Python
JSON-RPC backend → ui-tui/ Ink frontend).

## Current State (what we're replacing)

All in `/home/harsh/docker/overkill/`:

```
cmd/overkill/tui.go          — 1921 lines, runTUI boot function, Bubble Tea program
pkg/tui/tui.go               — 3329 lines, appModel (Bubble Tea Model)
pkg/tui/components/chat/     — message display, editor, autocomplete, history
pkg/tui/components/sidebar/  — files, cost, session, todo panels
pkg/tui/components/dialog/   — commands, models, sessions, theme, help, settings, etc.
pkg/tui/components/status/   — status bar, toast notifications
pkg/tui/components/animation/— boot animation, gate sequence
pkg/tui/components/spinner/  — loading spinner
pkg/tui/components/onboarding/ — first-run wizard
pkg/tui/components/viewer/   — file viewer / split view
pkg/tui/components/logo/     — ASCII logo
pkg/tui/layout/              — layout engine
pkg/tui/page/                — page routing
pkg/tui/theme/               — theming
pkg/tui/styles/              — lipgloss styles
```

## Phased Implementation (each phase = one OpenCode delegate_task)

Each phase builds on the previous. OpenCode gets ONE phase at a time to avoid crashing.

---

### Phase 0 — JSON-RPC Backend API

**Goal:** Expose the Go agent as a JSON-RPC server that the Ink TUI will consume.

**Files to create in Go:**
- `internal/api/server.go` — JSON-RPC over stdio (reads from stdin, writes to stdout)
- `internal/api/handlers.go` — handler stubs for each method
- `internal/api/types.go` — request/response types shared with frontend

**Methods to expose (start minimal):**
```
agent.send(message: string) → stream of chunks
agent.abort() → void
session.list() → Session[]
session.create(name?: string) → Session
session.load(id: string) → Session  
session.delete(id: string) → void
config.get() → Config
config.update(patch: Partial<Config>) → Config
providers.list() → Provider[]
models.list(provider: string) → Model[]
tools.list() → Tool[]
status.health() → HealthStatus
```

**Key constraints:**
- Communication over stdout/stdin (the Ink process spawns the Go binary)
- Or over a localhost TCP port (simpler for development)
- All streaming responses use newline-delimited JSON
- No BadgerDB — session store moves to SQLite or stays in memory for P0

**What we DON'T touch:**
- The Bubble Tea TUI files (they stay until Phase 5 cleanup)
- Internal packages (agent, providers, tools) — they stay exactly as-is

---

### Phase 1 — Ink Project Scaffold

**Goal:** Set up the TypeScript Ink project that can connect to the Go JSON-RPC backend and display "connected."

**Directory:** `/home/harsh/docker/overkill/tui/` (new directory, alongside pkg/tui/)

**Files to create:**
```
tui/package.json            — deps: ink, react, @hermes/ink (or vanilla ink)
tui/tsconfig.json
tui/src/entry.tsx           — main: spawn Go backend, connect, render <App/>
tui/src/app.tsx             — top-level component with providers
tui/src/backend/client.ts   — JSON-RPC client (talks to Go)
tui/src/backend/types.ts    — shared types
tui/src/components/status.tsx — connection status indicator
```

**Key decisions:**
- Use vanilla `ink` (not @hermes/ink — that's Hermes-specific)
- Use `ink-text-input` for input fields
- Use `nanostores` for state (same as Hermes)
- Use `unicode-animations` for polish
- Communication: spawn Go binary via `child_process`, talk over stdout/stdin JSON-RPC

**Verification:** Running `cd tui && npm start` should show "Connected to Overkill v0.1.0" in Ink.

---

### Phase 2 — Chat View (Core)

**Goal:** The main chat interface — message list, streaming display, input field.

**Components to create:**
```
tui/src/components/chat/
  message-list.tsx    — scrollable message history
  message.tsx         — single message bubble (user/assistant/system)
  streaming-text.tsx  — animated streaming text display
  input.tsx           — multiline input with submit
  prompt.tsx          — prompt bar with model indicator + send button
```

**Features:**
- Messages render with role colors (user=cyan, assistant=white, system=dim)
- Streaming text animates with unicode-animations
- Input supports multiline (Shift+Enter for newline, Enter to send)
- `/` commands parse and open command dialog (Phase 3)
- Markdown rendering for assistant messages (at least bold, italic, code)

**Backend methods needed:**
- `agent.send(message)` → streaming JSON chunks
- `agent.history(sessionId)` → Message[]

**Verification:** Can type a message, send to Go backend, see response streaming in.

---

### Phase 3 — Dialogs & Commands

**Goal:** All overlay dialogs — command palette, model switcher, session manager, settings.

**Components to create:**
```
tui/src/components/dialogs/
  command-palette.tsx   — fuzzy-search command palette (Ctrl+K)
  model-switcher.tsx    — provider/model picker
  session-manager.tsx   — list/create/delete sessions
  settings.tsx          — config editor (basic)
  help.tsx              — keyboard shortcuts reference
  theme-picker.tsx      — theme switcher
  permissions.tsx       — tool permission dialog
  dialog-container.tsx  — overlay wrapper with backdrop
```

**Backend methods needed:**
- `providers.list()`, `models.list(provider)`
- `session.list()`, `session.create()`, `session.delete()`
- `config.get()`, `config.update(patch)`

**Verification:** Ctrl+K opens command palette, can switch models, create sessions.

---

### Phase 4 — Sidebar & Tool Output

**Goal:** Sidebar panels (sessions, files, todo, cost) and tool execution display.

**Components to create:**
```
tui/src/components/sidebar/
  sidebar.tsx          — collapsible sidebar container
  session-panel.tsx    — session list
  files-panel.tsx      — workspace files (read-only for now)
  todo-panel.tsx       — todo list from agent
  cost-panel.tsx       — token usage + cost

tui/src/components/tools/
  tool-output.tsx      — tool execution result display
  diff-viewer.tsx      — diff display for file changes
  shell-output.tsx     — terminal output display
```

**Backend methods needed:**
- `workspace.files(path?)` → FileEntry[]
- `agent.cost(sessionId)` → CostSummary
- `agent.todos(sessionId)` → Todo[]
- Tool outputs come through `agent.send()` stream

**Verification:** Sidebar toggles with Ctrl+B, shows sessions/cost/files.

---

### Phase 5 — Theme, Polish & Cleanup

**Goal:** Theme system, animations, keyboard shortcuts, remove Bubble Tea.

**Tasks:**
- [ ] Theme system with dark/light/catppuccin presets
- [ ] Boot animation (ASCII logo + loading bar)
- [ ] Keyboard shortcut system (global + contextual)
- [ ] Toast notification system
- [ ] Mouse support for clickable elements
- [ ] Error boundaries and crash recovery
- [ ] Remove `cmd/overkill/tui.go` and `pkg/tui/` directory
- [ ] Wire `overkill` binary to launch Ink TUI instead of Bubble Tea
- [ ] Update AGENTS.md

**Verification:** Full feature parity with old Bubble Tea TUI, but in Ink.

---

## Constraints

1. **One phase per OpenCode run** — never feed more than one phase at a time
2. **Backend-first** — Phase 0 must complete before Phase 1 starts
3. **Go internal packages stay untouched** — only add the API layer, don't refactor internals
4. **BadgerDB removal is optional** — Phase 0 can use BadgerDB temporarily, or switch to SQLite
5. **Existing Bubble Tea TUI stays until Phase 5** — no breaking the running app
6. **Hermes TUI is the reference** — look at `/home/harsh/.hermes/hermes-agent/ui-tui/` for patterns

## Success Criteria

- `overkill` command launches Ink TUI instead of Bubble Tea
- All existing TUI functionality works (chat, sessions, commands, settings, sidebar)
- No BadgerDB corruption issues
- RAM usage is lower than Bubble Tea (target: <50MB for TUI process)
- Works over SSH/Termius without rendering glitches
