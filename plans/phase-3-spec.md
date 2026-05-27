# Phase 3: Dialogs & Commands

## Goal
Add overlay dialogs: command palette (Ctrl+K), model switcher, session manager, and base dialog infrastructure.

## Files to Create/Modify

### 1. `tui/src/components/dialogs/dialog-container.tsx`
Reusable overlay dialog wrapper:
- Props: `{ open: boolean, onClose: () => void, title: string, children }`
- Renders a bordered box overlay on top of the main content
- Semi-transparent backdrop effect (using dim colors)
- Escape key closes
- Title bar at top with the dialog name
- Fixed position in center of screen (calculate from terminal dimensions)
- Minimum width: 40, maximum width: terminal width - 4

### 2. `tui/src/components/dialogs/command-palette.tsx`
Ctrl+K fuzzy-search command palette:
- Shows list of commands with fuzzy filtering
- Commands: "Switch Model", "New Session", "Switch Session", "Settings", "Help", "Quit"
- Each command has: title, description, keybind hint
- Navigate with arrow keys, Enter to select, Escape to close
- Filter as you type — fuzzy match against title and description
- Highlight matching characters in results

### 3. `tui/src/components/dialogs/model-switcher.tsx`
Provider/model picker:
- Left panel: list of providers
- Right panel: list of models for selected provider
- Fetches providers and models from backend on open
- Enter or click to select model
- Shows currently active model with "●" indicator
- Model info: name, context window size, capabilities (tools/vision icons)

### 4. `tui/src/components/dialogs/session-manager.tsx`
Session list/create/delete:
- Lists all sessions from backend
- Shows: title, folder, last updated, model
- Enter to switch to session
- 'n' to create new session (prompt for folder)
- 'd' to delete session (with confirmation)
- Current session highlighted with "●"

### 5. `tui/src/hooks/use-dialogs.ts`
Dialog state management:
- `openDialog: string | null`
- `open(name: string): void`
- `close(): void`
- `toggle(name: string): void`

### 6. `tui/src/hooks/use-keybindings.ts`
Global keyboard shortcut system:
- `register(key: string, handler: () => void, context?: string): void`
- `unregister(key: string): void`
- Context-aware: shortcuts only fire when their context is active
- Built-in shortcuts: Ctrl+K (command palette), Ctrl+C (quit), Escape (close dialog)

### 7. Modify `tui/src/app.tsx`
- Add DialogProvider context
- Add useKeybindings for global shortcuts
- Render active dialog overlay on top of ChatView
- Ctrl+K opens command palette
- Ctrl+C quits (with double-tap safety)

### 8. Modify `tui/src/components/chat/prompt.tsx`
- Add "Ctrl+K for commands" hint on the right side when input is empty

## Backend Methods Expected
- `providers.list` → `{ providers: ProviderInfo[] }` (from Phase 0 types)
- `models.list` → `{ models: ModelInfo[] }` (takes `{ provider: string }`)
- `session.list` → `{ sessions: SessionInfo[] }`
- `session.create` → `{ session: SessionInfo }`
- `session.delete` → `{ id: string }` returns void

## Constraints
- TypeScript compiles clean
- All dialogs use the DialogContainer wrapper
- Keyboard-first navigation (no mouse required, but mouse support is a bonus)
- Handle loading states (show spinner while fetching providers/sessions)
- Handle errors (show error message in dialog if fetch fails)
- Don't block the chat view — dialogs are overlays

## Verification
`npx tsc --noEmit` passes cleanly.
