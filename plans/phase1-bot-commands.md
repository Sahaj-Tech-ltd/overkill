# Phase 1: Bot Command Registration for Telegram

Add clickable slash commands to Overkill's Telegram bot using setMyCommands API.

## Tasks

### 1.1: Add API methods to client.go
In `internal/gateway/telegram/client.go`, add:

```go
// BotCommand is one entry in the command menu shown in Telegram's input bar.
type BotCommand struct {
    Command     string `json:"command"`
    Description string `json:"description"`
}

// SetMyCommands replaces the bot's command list for all chats.
func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
    body, _ := json.Marshal(map[string]any{"commands": commands})
    q := url.Values{}
    q.Set("commands", string(body))
    var resp struct {
        OK   bool   `json:"ok"`
        Desc string `json:"description"`
    }
    if err := c.do(ctx, "setMyCommands", q, &resp); err != nil {
        return err
    }
    if !resp.OK {
        return fmt.Errorf("telegram: setMyCommands: %s", resp.Desc)
    }
    return nil
}

// DeleteMyCommands removes all commands (for shutdown cleanup).
func (c *Client) DeleteMyCommands(ctx context.Context) error {
    var resp struct {
        OK   bool   `json:"ok"`
        Desc string `json:"description"`
    }
    if err := c.do(ctx, "deleteMyCommands", url.Values{}, &resp); err != nil {
        return err
    }
    if !resp.OK {
        return fmt.Errorf("telegram: deleteMyCommands: %s", resp.Desc)
    }
    return nil
}
```

### 1.2: Command list in bot.go
In `internal/gateway/telegram/bot.go`, add:

```go
// registeredCommands returns the slash commands for the Telegram bot menu.
// Keep in sync with dispatch.go:handleCommand.
func registeredCommands() []BotCommand {
    return []BotCommand{
        {Command: "help", Description: "Show available commands"},
        {Command: "sessions", Description: "List recent sessions"},
        {Command: "attach", Description: "Bind chat to a session ID"},
        {Command: "follow", Description: "Mirror TUI session"},
        {Command: "unfollow", Description: "Clear follow mode"},
        {Command: "new", Description: "Start a fresh session"},
        {Command: "end", Description: "Clear follow, keep binding"},
        {Command: "bm", Description: "Bookmark current session"},
        {Command: "estop", Description: "Emergency stop all agents"},
    }
}
```

### 1.3: Register on startup with retry
Add to Bot struct in bot.go (no new fields needed — just add method):

```go
// registerCommands pushes the slash-command menu to Telegram with retry.
// Run as a goroutine during startup so it never blocks message intake.
func (b *Bot) registerCommands(ctx context.Context) {
    cmds := registeredCommands()
    backoff := 2 * time.Second
    maxBackoff := 5 * time.Minute
    for {
        if err := b.Client.SetMyCommands(ctx, cmds); err != nil {
            b.Logger.Printf("telegram: setMyCommands: %v (retry in %s)", err, backoff)
            select {
            case <-ctx.Done():
                return
            case <-time.After(backoff):
            }
            backoff *= 2
            if backoff > maxBackoff {
                backoff = maxBackoff
            }
            continue
        }
        b.Logger.Printf("telegram: %d commands registered", len(cmds))
        return
    }
}
```

In `Bot.Run()`, add `go b.registerCommands(ctx)` before the poll loop:

```go
func (b *Bot) Run(ctx context.Context) error {
    go b.registerCommands(ctx)  // <-- ADD THIS LINE
    
    backoff := time.Second
    for {
        // ... existing poll loop ...
    }
}
```

## Build & Verify

```bash
cd /home/harsh/docker/overkill && go build ./...
```

```bash
cd /home/harsh/docker/overkill && go test ./internal/gateway/telegram/...
```

## Notes

- Follow existing do() pattern in client.go — url.Values, c.do(), unmarshal response
- Add `"encoding/json"` to imports if not already present
- registeredCommands() stays in bot.go (not a separate file) — keeps it simple
- registerCommands() runs async so message polling is never blocked by Telegram API downtime
- Don't touch dispatch.go or any other files — Phase 1 is client.go + bot.go only
