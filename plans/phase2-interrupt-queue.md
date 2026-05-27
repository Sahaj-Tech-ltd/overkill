# Phase 2: Message Queue + Per-Session Stop

Add interrupt support so when user sends a message while agent is busy, the current turn is interrupted and the new message is queued. Add /stop for per-session interrupt.

## Background

Currently `dispatcher.serialize()` blocks new messages with a striped mutex. If the agent is processing and user sends another message, it waits behind the lock. No interrupt, no queue.

Hermes solves this with:
- `_busy_input_mode = "interrupt"` — new messages interrupt the running agent
- `_pending_messages` dict — interrupted message stored and surfaced next turn
- `/stop` command — per-session stop (not killing all agents like /estop)

## Tasks

### 2.1: Add Interrupt() to AgentSender interface

In `internal/gateway/types.go`, add to the interface:

```go
type AgentSender interface {
    Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error)
    SetSessionID(id string)
    SessionID() string
    EStop()
    // Interrupt cancels the currently running stream for this agent.
    // Safe to call from any goroutine. No-op if no stream is running.
    Interrupt()
}
```

In `internal/agent/agent.go`, add Interrupt method. Check if agent already has a stream cancel function — if not, add one:

Look for existing `streamCancel` or `cancel` fields. If none exist, add:
```go
mu           sync.Mutex
streamCancel context.CancelFunc
```

Then:
```go
func (a *Agent) Interrupt() {
    a.mu.Lock()
    defer a.mu.Unlock()
    if a.streamCancel != nil {
        a.streamCancel()
        a.streamCancel = nil
    }
}
```

In Agent.Stream(), save the cancel func:
```go
func (a *Agent) Stream(ctx context.Context, in string) (<-chan StreamEvent, error) {
    ctx, cancel := context.WithCancel(ctx)
    a.mu.Lock()
    a.streamCancel = cancel
    a.mu.Unlock()
    // ... existing stream setup ...
}
```

In `cmd/overkill/gateway_cmd.go`, update the gatewayAgentAdapter:
```go
func (g *gatewayAgentAdapter) Interrupt() {
    g.a.Interrupt()
}
```

### 2.2: Make dispatcher interruptible

In `internal/gateway/dispatch.go`, replace the blocking `serialize()` with active-session tracking.

Add to Dispatcher struct:
```go
// active tracks which sessions currently have a running turn.
active   map[string]bool
activeMu sync.Mutex

// pending holds messages that interrupted a running turn.
// Surfaced as the first message of the next turn.
pending   map[string]string
pendingMu sync.Mutex
```

Initialize in NewDispatcher:
```go
func NewDispatcher(ag AgentSender, r *SessionRouter) *Dispatcher {
    return &Dispatcher{
        Agent:       ag,
        Router:      r,
        Logger:      log.New(io.Discard, "", 0),
        UpdateEvery: 750 * time.Millisecond,
        active:      make(map[string]bool),
        pending:     make(map[string]string),
    }
}
```

Replace the Handle() method's serialize logic. Remove the call to `d.serialize(sid, ...)` and the `serialize` method entirely (keep `stripeFor` for now). Replace with:

```go
func (d *Dispatcher) Handle(ctx context.Context, in Inbound, reply Reply) {
    text := strings.TrimSpace(in.Text)
    if text == "" && len(in.Images) == 0 {
        return
    }
    // Slash commands always route immediately — they're fast.
    if text != "" && strings.HasPrefix(text, "/") && len(in.Images) == 0 {
        d.handleCommand(ctx, in, reply, text)
        return
    }

    prompt := d.buildPrompt(ctx, in, text)
    if prompt == "" {
        return
    }
    sid := d.resolveSession(in)

    // Check if session is busy.
    d.activeMu.Lock()
    if d.active[sid] {
        // Session is busy — interrupt and queue the new message.
        d.pendingMu.Lock()
        d.pending[sid] = prompt
        d.pendingMu.Unlock()
        d.activeMu.Unlock()
        d.Agent.Interrupt()
        // Brief ack so user knows we caught their message.
        postCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
        handle, _ := reply.PostInitial(postCtx, in, "⏸️ interrupting — your message is queued…")
        cancel()
        if handle != "" {
            _ = reply.Final(ctx, handle, "⏸️ interrupted. your message is queued for the next turn.\n\ntap to edit before it sends.")
        }
        return
    }
    d.active[sid] = true
    d.activeMu.Unlock()

    defer func() {
        d.activeMu.Lock()
        delete(d.active, sid)
        d.activeMu.Unlock()
    }()

    // Check for queued message from a previous interrupt.
    d.pendingMu.Lock()
    queued := d.pending[sid]
    delete(d.pending, sid)
    d.pendingMu.Unlock()

    if queued != "" {
        prompt = queued + "\n\n[new message]\n" + prompt
    }

    d.runTurn(ctx, in, reply, sid, prompt)
}
```

Remove the old `serialize` method since it's no longer called:
```go
// DELETE the serialize method
```

### 2.3: Add /stop command

In `dispatch.go` handleCommand(), add:

```go
case "/stop":
    sid := d.resolveSession(in)
    d.pendingMu.Lock()
    queued := d.pending[sid]
    d.pendingMu.Unlock()
    d.Agent.Interrupt()
    if queued != "" {
        d.respond(ctx, in, reply, fmt.Sprintf("⏸️ stopped. queued message: %q", queued))
    } else {
        d.respond(ctx, in, reply, "⏸️ stopped. nothing queued.")
    }
```

Update helpText() to include:
```
"  /stop             stop the current turn (queues your next message)"
```

## Build & Verify

```bash
cd /home/harsh/docker/overkill && go build ./...
```

```bash
cd /home/harsh/docker/overkill && go test ./internal/gateway/... ./internal/agent/...
```

## Critical Notes

- The Agent.Stream() method needs to be checked — does it already have a context cancellation mechanism? Read agent.go to verify before implementing.
- The `serialize` method removal is safe because Handle() now manages active state directly.
- The active map + mutex replaces the striped lock pool — simpler and enables TryLock semantics.
- Don't touch the stripeFor method yet — it may still be used elsewhere.
