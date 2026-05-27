# TUI Streaming + Thinking + Status Bar Spec

## SSE Event Contract (shared between Go and TS)

All events are SSE with `event:` type and JSON `data:`.

### Event types

```
event: status     → {"phase": "context_fill"|"thinking"|"responding"|"tool_call"|"done"}
event: reasoning  → {"content": "..."}        // thinking block, streamed token-by-token
event: text       → {"content": "..."}        // assistant text, streamed token-by-token
event: tool_call  → {"name": "...", "input": {...}, "output": "..."}
event: done       → {"model": "...", "tokens": N, "tool_calls": N, "steps": N}
event: error      → {"message": "..."}
```

### SSE endpoint: `GET /stream?session_id=X&message=...`

## Go Changes (internal/api/)

1. **New handler: `handleAgentStream`** — accepts `{session_id, message}`, creates agent, calls `a.Stream(ctx, message)` which returns `<-chan StreamEvent`, fans out to SSE writer.
2. **New agent method: `Stream(ctx, message) (<-chan StreamEvent, error)`** — wraps the existing `Run()` loop but emits events as they happen instead of accumulating.
3. **StreamEvent type:**
```go
type StreamEvent struct {
    Type    string // "status", "reasoning", "text", "tool_call", "done", "error"
    Phase   string // for status events
    Content string
    ToolName string
    ToolInput  interface{}
    ToolOutput string
    Model   string
    Tokens  int
    Error   string
}
```
4. **Server.go:** Register `GET /stream` route, set SSE headers (`Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`).

## TypeScript Changes (tui/src/)

### 1. backend/client.ts — add SSE streaming
- New method: `streamCall(method, params): AsyncGenerator<StreamEvent>`
- Uses `EventSource` or `fetch` with `ReadableStream` to consume SSE

### 2. hooks/use-chat.ts — progressive rendering
- Change `sendMessage` to use `streamCall` instead of `call`
- Maintain a partial `assistantMsg` that updates on every `text` event
- Show the streaming message in MessageList via `streamingText` prop (already exists but unused)
- On `done` event, finalize the message

### 3. components/chat/thinking-block.tsx (new)
- Takes `reasoning: string` and `collapsed: boolean` props
- Shows a "🤔 Thinking..." header with expand/collapse toggle
- When expanded, shows the reasoning text in a dimmed bordered box
- Collapsed by default; user toggles with Enter/Space

### 4. components/chat/message.tsx — thinking integration
- Add `reasoning?: string` prop to MessageBubble
- When present, render `<ThinkingBlock>` above the message content
- Parse `event: reasoning` from the stream

### 5. components/status-bar.tsx — git branch + queued badge
- Add `gitBranch?: string` and `queuedMessages?: number` props
- Git branch: run `git rev-parse --abbrev-ref HEAD` on TUI startup, displayed left of time
- Queued badge: show count in brackets like `[2 queued]` next to model info, yellow when >0

## Acceptance Criteria
- [ ] Sending a message shows progressive text rendering (not a spinner then full dump)
- [ ] Reasoning/thinking blocks appear as collapsible sections
- [ ] Status bar shows git branch and queued message count
- [ ] `go build ./...` passes
- [ ] `npx tsc --noEmit` passes in `tui/`
- [ ] Status bar: `● connected │ deepseek/deepseek-v4-pro │ [2 queued] │ 07:21 │ main │ v0.2.0`
