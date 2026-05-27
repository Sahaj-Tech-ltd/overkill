# TUI Multiline + Gateway Robustness Plan

> **For butter:** Delegate each section to OpenCode. Auto-continue.

**Goal:** Replace single-line `ink-text-input` with Hermes-style multiline composer, add missing TUI QoL features, and harden Slack/Discord/WhatsApp gateways.

---

## Section 1: Multiline TUI Input (HIGHEST PRIORITY)

Replace `ink-text-input` in `tui/src/components/chat/input.tsx` with a custom multiline TextInput inspired by Hermes' `textInput.tsx` at `inspiration/hermes-agent/ui-tui/src/components/textInput.tsx`.

### What Hermes' TextInput does that Overkill needs:

| Feature | Hermes | Overkill (ink-text-input) |
|---|---|---|
| Multiline | ✅ Enter = newline | ❌ single-line |
| Cursor movement | ✅ left/right/up/down, word nav (Ctrl+arrows), home/end | ❌ basic only |
| Selection | ✅ Shift+arrows, Ctrl+A, copy/paste | ❌ none |
| Undo/redo | ✅ Ctrl+Z / Ctrl+Shift+Z | ❌ none |
| History | ✅ Up/down arrow recall | ❌ none |
| Paste handling | ✅ bracketed paste, async paste | ❌ basic only |
| Emacs keys | ✅ Ctrl+A/E/K/U/W | ❌ none |
| Submit | ✅ Ctrl+Enter (or Enter only on last line) | ❌ just Enter |
| Placeholder | ✅ dim text when empty | ✅ same |
| Focus/blur | ✅ terminal focus aware | ✅ basic |

### Implementation approach:

Don't copy the entire 1088-line Hermes TextInput — that's overkill. Build a focused multiline TextInput component:

1. Create `tui/src/components/chat/multiline-input.tsx`
2. Port key features in order:
   - Multiline with Enter=newline, Ctrl+Enter=submit
   - Cursor movement (arrow keys, Ctrl+arrows for word nav, home/end)
   - Paste support (bracketed paste detection)
   - Input history (up/down arrow)
   - Selection + copy (Shift+arrows)
   - Ctrl+A/E/K/U Emacs keys
3. Replace `ink-text-input` in `input.tsx` with the new component
4. Keep Ctrl+Enter submit (already done)

### Files:
- Create: `tui/src/components/chat/multiline-input.tsx`
- Modify: `tui/src/components/chat/input.tsx`
- Reference: `inspiration/hermes-agent/ui-tui/src/components/textInput.tsx` (study, don't copy)

---

## Section 2: Missing TUI QoL Features

Features Hermes TUI has that Overkill should add:

### 2.1: Streaming Message Display
- Hermes: `streamingAssistant.tsx` + `streamingMarkdown.tsx` — progressive markdown rendering
- Overkill: `message.tsx` — basic text display
- Add: Token-by-token streaming display, thinking/reasoning toggle

### 2.2: Tool Output in Sidebar
- Hermes: Tool results shown inline with toggle
- Overkill: `tool-output.tsx` — basic, no diff highlighting
- Add: Syntax highlighting for code, diff rendering, collapse/expand

### 2.3: Queued Messages Indicator
- Hermes: `queuedMessages.tsx` — shows "1 message queued" badge
- Overkill: Nothing visual
- Add: Badge/pill showing queued count in the TUI status bar

### 2.4: Model Picker (hotkey)
- Hermes: `modelPicker.tsx` — Ctrl+M overlay
- Overkill: `model-switcher.tsx` — exists
- Exists ✅

### 2.5: Command Palette
- Hermes: `/` triggers slash command palette with autocomplete
- Overkill: `command-palette.tsx` — exists
- Exists ✅

### 2.6: Status Bar Polish
- Hermes: Model name, token usage, cost, session ID, git branch
- Overkill: `status-bar.tsx` — basic (model + provider)
- Add: Token count, cost estimate, git branch, connection indicator

### 2.7: Input History
- Navigate previous inputs with Up/Down
- Add to multiline-input component

### 2.8: Mouse Support
- Click to position cursor, double-click select word, triple-click select line
- Add to multiline-input component

---

## Section 3: Gateway Robustness

### 3.1: Slack Gateway (NEW)
Overkill has NO Slack gateway. Build one.

- Create: `internal/gateway/slack/bot.go`
- Use Slack RTM or Events API (prefer Events API with socket mode)
- Follow existing gateway pattern: implement `gateway.Channel` + `gateway.Reply`
- Features: slash commands, thread support, message editing, reaction support
- Reference: Hermes `gateway/platforms/slack.py`

### 3.2: Discord Gateway Polish
Overkill has `internal/gateway/discord/bot.go`. Audit and harden:

- Bot command registration (Discord slash commands via REST API)
- Rich embeds for tool output
- Thread support
- Rate limit handling (Discord is aggressive with 429s)
- Message editing (update thinking→final)
- Typing indicator (Discord supports it)

### 3.3: WhatsApp Gateway Polish
Overkill has cloud + whatsmeow. Harden:

- Cloud API: media handling, template messages, webhook verification
- Whatsmeow: reconnection logic, pairing QR flow, multi-device
- Message queue reliability

### 3.4: Gateway-common Features
- Health check endpoint
- Graceful shutdown with drain
- Reconnection backoff (shared between all gateways)
- Message deduplication
- Rate limit tracking

---

## Build & Verify

```bash
cd /home/harsh/docker/overkill && go build ./...
cd /home/harsh/docker/overkill/tui && npx tsc --noEmit
cd /home/harsh/docker/overkill && go test ./...
```
