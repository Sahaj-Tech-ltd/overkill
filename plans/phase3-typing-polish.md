# Phase 3: Native Typing Indicator + Polish

Add sendChatAction typing indicator, response time logging, and the Hermes-style visible interrupt pattern.

## Background

Currently the dispatcher edits a "⏳ thinking…" placeholder. Telegram has a native `sendChatAction(chat_id, 'typing')` that shows animated "…" in the chat header. Hermes uses both — native indicator + editing placeholder.

Also: when a user interrupts, the queued message should appear as a *visible message in the chat* (not just internal storage). The agent continues editing its response above the user's interrupting message.

## Tasks

### 3.1: Add sendChatAction to Telegram client

In `internal/gateway/telegram/client.go`, add:

```go
// SendChatAction shows a status action in the chat (typing, upload_photo, etc.).
// The indicator auto-expires after 5 seconds — callers should refresh.
func (c *Client) SendChatAction(ctx context.Context, chatID int64, action string) error {
    q := url.Values{}
    q.Set("chat_id", strconv.FormatInt(chatID, 10))
    q.Set("action", action)
    var resp struct {
        OK   bool   `json:"ok"`
        Desc string `json:"description"`
    }
    if err := c.do(ctx, "sendChatAction", q, &resp); err != nil {
        return err
    }
    if !resp.OK {
        return fmt.Errorf("telegram: sendChatAction: %s", resp.Desc)
    }
    return nil
}
```

### 3.2: Add StartTyping to Reply interface

In `internal/gateway/types.go`, add to Reply interface:

```go
type Reply interface {
    PostInitial(ctx context.Context, in Inbound, text string) (handle string, err error)
    Update(ctx context.Context, handle, text string) error
    Final(ctx context.Context, handle, text string) error
    Error(ctx context.Context, handle string, err error) error
    // StartTyping begins the native typing indicator if the channel supports it.
    // Returns a stop function that clears the indicator. Channels without
    // native typing support return a no-op stop function.
    StartTyping() (stop func())
}
```

### 3.3: Implement StartTyping in telegramReply

In `internal/gateway/telegram/bot.go`, add to telegramReply:

```go
// StartTyping implements gateway.Reply. Sends the native Telegram typing
// indicator and refreshes it every 4s until stop() is called.
func (r *telegramReply) StartTyping() (stop func()) {
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan struct{})
    go func() {
        defer close(done)
        // Initial send
        _ = r.client.SendChatAction(ctx, r.chatID, "typing")
        ticker := time.NewTicker(4 * time.Second) // refresh before 5s expiry
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                _ = r.client.SendChatAction(ctx, r.chatID, "typing")
            }
        }
    }()
    return func() {
        cancel()
        <-done
    }
}
```

### 3.4: Wire StartTyping into dispatcher

In `internal/gateway/dispatch.go`, in `runTurn()`:

```go
func (d *Dispatcher) runTurn(ctx context.Context, in Inbound, reply Reply, sessionID, text string) {
    stopTyping := reply.StartTyping()
    defer stopTyping()
    
    start := time.Now()
    defer func() {
        elapsed := time.Since(start).Round(time.Millisecond)
        d.Logger.Printf("turn complete: chat=%s session=%s time=%s", in.ChatKey, sessionID, elapsed)
    }()
    
    postCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
    handle, err := reply.PostInitial(postCtx, in, "⏳ thinking…")
    cancel()
    // ... rest of existing code ...
}
```

### 3.5: Add no-op StartTyping for non-Telegram channels

Discord, WhatsApp, and bridge channels don't support native typing. Add a no-op helper.

In a shared location OR just implement on each reply type. The cleanest: add a helper in types.go:

```go
// NoopTyping returns a no-op stop function for channels without native typing.
func NoopTyping() (stop func()) { return func() {} }
```

Then each non-Telegram Reply can do `return gateway.NoopTyping()`.

Actually simpler: just add StartTyping to each reply implementation. For discord/bridge/whatsapp:

```go
func (r *discordReply) StartTyping() (stop func()) { return func() {} }
func (r *bridgeReply) StartTyping() (stop func()) { return func() {} }
```

Find all Reply implementations and add the no-op.

### 3.6: Hermes-style visible interrupt message

In `dispatch.go` Handle(), when a session is busy, instead of just storing the interrupt internally:

BEFORE (current):
```go
if d.active[sid] {
    d.pendingMu.Lock()
    d.pending[sid] = prompt
    d.pendingMu.Unlock()
    d.activeMu.Unlock()
    d.Agent.Interrupt()
    // Brief ack — gets replaced later
    ...
}
```

AFTER (Hermes-style):
```go
if d.active[sid] {
    d.pendingMu.Lock()
    d.pending[sid] = prompt
    d.pendingMu.Unlock()
    d.activeMu.Unlock()
    d.Agent.Interrupt()
    // Post the interrupting message as a REAL message in chat so
    // the user can see it and edit it. The agent continues updating
    // its message above this one. This is the Hermes pattern.
    postCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
    handle, err := reply.PostInitial(postCtx, in, "⏸️ " + prompt)
    cancel()
    if err != nil {
        d.Logger.Printf("dispatch: interrupt ack: %v", err)
    } else if handle != "" {
        // Leave the message visible — don't call Final to replace it.
        // The user's interrupted text stays at the bottom of the chat.
        d.Logger.Printf("dispatch: interrupt posted as msg %s", handle)
    }
    return
}
```

## Build & Verify

```bash
cd /home/harsh/docker/overkill && go build ./...
```

```bash
cd /home/harsh/docker/overkill && go test ./internal/gateway/...
```

## Notes

- sendChatAction auto-expires after 5s — the 4s refresh ensures no gap
- StartTyping goroutine is cleaned up via defer stopTyping() in runTurn
- Non-Telegram channels need no-op StartTyping — check all reply implementations
- The Hermes-style interrupt posts the user's text as a real visible message — this is the key UX improvement over the current internal queue
