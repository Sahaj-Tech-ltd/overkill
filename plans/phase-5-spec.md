# Phase 5: Theme, Polish & Cleanup

## Goal
Add theme system, boot animation, toast notifications, and wire up the Go backend binary to auto-launch. Polish the TUI to feel like a real product.

## Tasks

### 1. Theme System
Create `tui/src/hooks/use-theme.ts` and `tui/src/themes/`:
- Three built-in themes: dark (default), light, catppuccin-mocha
- Each theme defines: background, text, accent, muted, error, success, warning, border colors
- `useTheme()` hook provides: `theme`, `setTheme(name)`, `themes` list
- ThemeProvider wraps App
- Persist theme choice (for now: just in-memory, localStorage isn't available in terminal)

### 2. Boot Animation  
Modify `tui/src/entry.tsx`:
- Show ASCII art "OVERKILL" logo on startup for 2 seconds
- Below it: "The vibe-coding agent" with typewriter effect
- Loading bar that fills over 2s
- Then transitions to main App
- Use `unicode-animations` package if available, otherwise manual setTimeout

### 3. Toast Notifications
Create `tui/src/hooks/use-toast.ts` and `tui/src/components/toast.tsx`:
- `toast.show(message, variant?, duration?)` where variant is 'info'|'success'|'warning'|'error'
- Toasts appear at top-right corner, auto-dismiss after 3s
- Stack multiple toasts vertically
- Colored left border by variant (cyan/green/yellow/red)

### 4. Error Boundaries
Modify `tui/src/entry.tsx`:
- Wrap App in an error boundary that catches render errors
- Shows red "Something went wrong" screen with error message
- "Press any key to restart" option

### 5. Status Bar Polish
Modify `tui/src/components/status-bar.tsx`:
- Add version number on right: `v0.2.0`
- Add current time on right (optional, updating every 30s)
- Colored background matching theme

### 6. Keyboard Shortcuts Summary
Modify `tui/src/components/dialogs/command-palette.tsx`:
- Add "Keyboard Shortcuts" command that shows a help dialog
- List all shortcuts: Ctrl+K palette, Ctrl+B sidebar, Ctrl+L clear, Ctrl+C quit, Esc close

### 7. Cleanup
- Remove unused code/comments from any component
- Ensure all imports use `.ts` / `.tsx` extensions consistently
- Verify `npx tsc --noEmit` passes clean

## Constraints
- Don't add new npm dependencies (use existing: react, ink, ink-text-input, nanostores, unicode-animations)
- Boot animation must not block — app should render even if animation fails
- Theme system must be lightweight — just color mappings, no CSS-in-JS

## Verification
- `npx tsc --noEmit` passes
- `npm start` should show boot animation → main app
