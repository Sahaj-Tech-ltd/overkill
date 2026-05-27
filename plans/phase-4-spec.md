# Phase 4: Sidebar & Tool Output

## Goal
Add a collapsible sidebar with session list, tool output viewer, and a diff/shell output display for agent tool calls.

## Files to Create/Modify

### 1. `tui/src/components/sidebar/sidebar.tsx`
Collapsible sidebar container:
- Props: `{ visible: boolean, onToggle: () => void, children }`
- Width: 30 chars when visible, 0 when hidden
- Right border separating from main content (using `│` chars)
- Toggle with Ctrl+B
- Shows section tabs at top: Sessions | Tools | Files
- Active tab has inverted colors

### 2. `tui/src/components/sidebar/session-panel.tsx`
Session list in sidebar (reuses the session data, simpler than the dialog):
- Lists sessions with title, folder, time
- Click or Enter to switch
- Shows agent status (idle/running) with colored dot

### 3. `tui/src/components/sidebar/tool-output.tsx`
Tool execution result display:
- Shows the last N tool calls and their results
- Each entry: tool name, status (success/error/running), truncated output
- Expandable: Enter on a tool to see full output in a scrollable view
- Running tools show a spinner
- Auto-scrolls to latest

### 4. `tui/src/components/sidebar/diff-viewer.tsx`
Simple diff display:
- Props: `{ oldText: string, newText: string }`
- Shows added lines in green with `+`, removed lines in red with `-`
- Context lines in dim white
- Limited to 20 lines max, scrollable

### 5. `tui/src/hooks/use-sidebar.ts`
Sidebar state:
- `visible: boolean`
- `activeTab: 'sessions' | 'tools' | 'files'`
- `toggle(): void`
- `setTab(tab): void`

### 6. Modify `tui/src/app.tsx`
- Add sidebar with Ctrl+B toggle
- Show sidebar alongside chat when visible (horizontal split)
- Wire up useSidebar hook
- Pass backend client to sidebar panels

### 7. Modify `tui/src/hooks/use-keybindings.ts`
- Add Ctrl+B for sidebar toggle

## Backend Methods Expected
- `session.list` → already wired from Phase 3
- Tool outputs come through agent.send response or separate endpoint
- For now: parse tool results from the response if available

## Constraints
- Sidebar width: fixed 30 chars when open
- Don't break the chat view when sidebar is open (horizontal flex split)
- Handle no-tool-output state gracefully
- TypeScript compiles clean

## Verification
`npx tsc --noEmit` passes cleanly.
