# Slack Gateway for Overkill

Build a Slack gateway using Slack's Events API with Socket Mode (no HTTP endpoint needed). Follow existing Overkill gateway patterns.

## Architecture

Socket Mode uses a WebSocket connection authenticated with a Slack app-level token. Events arrive as JSON over the socket — no public HTTP endpoint needed.

### Dependencies

Add `github.com/slack-go/slack` — the official Go Slack SDK. Already used by Hermes in Python; the Go equivalent is battle-tested.

```bash
cd /home/harsh/docker/overkill && go get github.com/slack-go/slack
```

### Files

- Create: `internal/gateway/slack/bot.go` — Slack Bot (implements gateway.Channel)
- Create: `internal/gateway/slack/client.go` — Web API wrapper (minimal, just what we need)
- Modify: `cmd/overkill/gateway_cmd.go` — wire Slack into gateway command
- Modify: `internal/config/config.go` — add Slack config section

### Config (in config.go)

```go
type SlackConfig struct {
    Enabled      bool   `toml:"enabled"`
    BotToken     string `toml:"bot_token"`     // xoxb-...
    AppToken     string `toml:"app_token"`     // xapp-... (Socket Mode)
    AllowedUsers []string `toml:"allowed_users"` // user IDs; empty = all
}
```

Add to GatewayConfig:
```go
Slack SlackConfig `toml:"slack"`
```

### Bot (bot.go)

Socket Mode flow:
1. Connect to `wss://wss-primary.slack.com/link/?ticket=...` using app token
2. Receive `events_api` envelope with `type: "message"` events
3. Dispatch to gateway.Dispatcher
4. Send replies via `chat.postMessage` Web API

Key types:
```go
type SlackBot struct {
    Client     *SlackClient
    Dispatcher *gateway.Dispatcher
    Allowed    map[string]bool
    Logger     *log.Logger
}

func (b *SlackBot) Name() string { return "slack" }

func (b *SlackBot) Run(ctx context.Context) error {
    // 1. apps.connections.open → get WebSocket URL
    // 2. Connect WebSocket, read events loop
    // 3. For message events: build Inbound, call b.Dispatcher.Handle()
    // 4. Reconnect with backoff on disconnect
}
```

### Reply (in bot.go)

```go
type slackReply struct {
    client  *SlackClient
    channel string
    ts      string // message timestamp for edits
}

func (r *slackReply) PostInitial(ctx context.Context, _ gateway.Inbound, text string) (string, error) {
    ts, err := r.client.PostMessage(ctx, r.channel, text)
    if err != nil {
        return "", err
    }
    r.ts = ts
    return ts, nil
}

func (r *slackReply) Update(ctx context.Context, handle, text string) error {
    return r.client.UpdateMessage(ctx, r.channel, handle, text)
}

func (r *slackReply) Final(ctx context.Context, handle, text string) error {
    return r.Update(ctx, handle, text)
}

func (r *slackReply) Error(ctx context.Context, _ string, err error) error {
    _, postErr := r.client.PostMessage(ctx, r.channel, "⚠️ "+err.Error())
    return postErr
}

func (r *slackReply) StartTyping() (stop func()) { return func() {} }
```

### Client (client.go) — minimal Web API wrapper

```go
type SlackClient struct {
    BotToken string
    HTTP     *http.Client
}

// PostMessage sends a message. Returns the message timestamp.
func (c *SlackClient) PostMessage(ctx context.Context, channel, text string) (string, error)

// UpdateMessage edits an existing message by timestamp.
func (c *SlackClient) UpdateMessage(ctx context.Context, channel, ts, text string) error

// OpenSocket connects Socket Mode and returns a WebSocket URL.
func (c *SlackClient) OpenSocket(ctx context.Context) (string, error)
```

Use `chat.postMessage`, `chat.update`, `apps.connections.open` REST endpoints. Base URL: `https://slack.com/api/`.

### Wire in gateway_cmd.go

```go
if s := cfg.Gateways.Slack; s.Enabled || os.Getenv("SLACK_BOT_TOKEN") != "" {
    token := s.BotToken
    if token == "" { token = os.Getenv("SLACK_BOT_TOKEN") }
    appToken := s.AppToken
    if appToken == "" { appToken = os.Getenv("SLACK_APP_TOKEN") }
    if token == "" || appToken == "" {
        logger.Printf("slack: enabled but tokens missing; skipping")
    } else {
        client := slack.NewClient(token, appToken)
        sb := slack.NewBot(client, disp, s.AllowedUsers)
        sb.Logger = logger
        hub.Add(sb)
        logger.Printf("slack: registered (socket mode)")
    }
}
```

### Build & Verify

```bash
cd /home/harsh/docker/overkill && go build ./...
go test ./internal/gateway/slack/...
```

## Notes

- Socket Mode requires both bot token (xoxb-) and app token (xapp-)
- The app needs these OAuth scopes: `chat:write`, `chat:read`, `connections:write`
- Enable Socket Mode in Slack app settings (not HTTP endpoints)
- Handle Slack's aggressive rate limiting (tier 3 = 1 msg/sec for most workspaces)
- Reconnect WebSocket with exponential backoff on disconnect
- Filter bot's own messages to avoid echo loops
- Support both DM and channel messages
