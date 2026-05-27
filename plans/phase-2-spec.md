# Phase 2: Chat View (Core)

## Goal
Build the main chat interface: message list with streaming, input field, and model indicator. When the user types a message and hits Enter, send it to the Go backend via JSON-RPC and display the response.

## Files to Create/Modify

### 1. `tui/src/components/chat/message.tsx`
Single message bubble component:
- Props: `{ role: 'user' | 'assistant' | 'system', content: string, streaming?: boolean }`
- User messages: white text, right-aligned feel
- Assistant messages: cyan prefix "Overkill >", left-aligned
- System messages: dim yellow text
- Streaming messages: show a blinking cursor "▊" at end when streaming=true
- Markdown rendering: at minimum support **bold** and `inline code` and ```code blocks```
- Wrap text to terminal width

### 2. `tui/src/components/chat/message-list.tsx`
Scrollable message history:
- Props: `{ messages: Message[], streamingText?: string }`
- Uses Ink's Box with flexDirection="column"
- Renders all messages from the array
- If streamingText is non-empty, renders an extra assistant message with streaming=true
- Auto-scrolls to bottom (show latest messages within terminal height)

### 3. `tui/src/components/chat/input.tsx`
Multiline text input:
- Props: `{ onSubmit: (text: string) => void, disabled?: boolean }`
- Uses `ink-text-input` for input handling
- Shows "Type a message..." placeholder when empty
- Enter submits, Shift+Enter adds newline (note: this may be limited by ink-text-input)
- Disabled state: dim the input, show "Thinking..." instead of placeholder
- Shows character count? (optional, keep it minimal)

### 4. `tui/src/components/chat/prompt.tsx`
The bottom bar with model info and input:
- Shows current model/provider on the left: `◉ deepseek/deepseek-v4-pro`
- Embeds the Input component
- Height: 3 lines minimum

### 5. `tui/src/components/chat/chat-view.tsx`
Composes everything:
- Fetches message history from backend on mount
- Manages local message array
- Sends messages via backend JSON-RPC
- Handles streaming responses (for now: batch mode — send, wait, show result. Streaming SSE comes later)
- Keyboard: Ctrl+L clears chat, Escape cancels current request

### 6. Modify `tui/src/app.tsx`
- Replace the placeholder "Overkill TUI" center screen with ChatView
- Keep StatusBar at bottom
- Pass backend client down via React context or props

### 7. `tui/src/hooks/use-chat.ts`
Chat state management hook:
- messages: Message[]
- sendMessage(text: string): Promise<void>
- clearChat(): void
- isLoading: boolean
- error: string | null
- Calls backend.call('agent.send', { message, session_id })

## Backend Methods Expected
- `agent.send` — takes `{message, session_id?}` returns `{response, tool_calls, total_tokens, model}`
- For now: batch mode (send full message, wait for full response). SSE streaming comes in Phase 3.

## Constraints
- TypeScript compiles clean (`npx tsc --noEmit`)
- Uses existing `useBackend` hook for connection
- No new npm dependencies unless absolutely needed
- Ink components only — no DOM/web APIs
- Handle errors gracefully (show error messages, don't crash)
- Terminal dimensions: use `useStdout()` from ink for width

## Verification
1. `npx tsc --noEmit` passes
2. `npm start` should show the chat interface (will show "Disconnected" since Go backend isn't started, but shouldn't crash)
