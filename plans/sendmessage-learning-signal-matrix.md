# Send Message Tool + Learning from Corrections + Signal/Matrix

## 1. Send Message Tool (cross-platform) — `internal/tools/messaging/`

New tool: `send_message` — sends messages to any connected gateway platform.

### Tool interface:
```go
type SendMessageTool struct {
    gateways map[string]gateway.Channel
}
func (t *SendMessageTool) Name() string { return "send_message" }
```

Input: `{"platform": "telegram"|"discord"|"slack", "target": "chat_id_or_channel", "message": "..."}`
Output: `{"sent": true, "platform": "telegram", "message_id": "..."}`

### Supported platforms:
- **Telegram:** Use existing `internal/gateway/telegram/client.go` — `SendMessage(chatID, text)`
- **Discord:** Use existing `internal/gateway/discord/bot.go` — `ChannelMessageSend(channelID, text)`
- **Slack:** Use `internal/gateway/slack/bot.go` — `PostMessage(channelID, text)`

### File: `internal/tools/messaging/send.go` (new package)
### Registration: `cmd/overkill/tui.go` and `cmd/overkill/run.go` — register tool

## 2. Learning from Corrections — `internal/learning/`

When the user corrects overkill (e.g., "no, that's wrong, do it this way"), capture the correction and use it to improve future responses.

### Mechanism:
- **Correction detection:** When user message starts with "no,", "wrong,", "actually,", "instead,", "correct:", or follows an error/correction pattern
- **Store:** Save correction pair `(context, wrong_response, correct_response)` to a BadgerDB store
- **Retrieve:** On future similar queries, inject top-k corrections into the system prompt as "User preferences from past corrections"
- **Simple, not over-engineered:** Just embedding-free keyword match for retrieval — don't build a full RAG

### Files:
- `internal/learning/correction.go` — detection + storage
- `internal/learning/store.go` — BadgerDB-backed persistence
- Wire into agent loop: check for corrections after each assistant response

## 3. Signal Gateway — `internal/gateway/signal/`

New gateway for Signal messaging via signald or signal-cli REST API.

### Approach: signal-cli REST mode
- Run `signal-cli` in daemon mode with `--rest-api` flag
- Python subprocess management or docker sidecar
- For now: Go client that calls localhost REST API

### Minimal viable:
- `internal/gateway/signal/bot.go` — implements `gateway.Channel`
- Receive: poll `/v1/receive/{account}` endpoint
- Send: POST `/v2/send` with JSON body
- Health: check signal-cli is running

### Files:
- `internal/gateway/signal/bot.go`
- `cmd/overkill/gateway_cmd.go` — wire signal into gateway command

## 4. Matrix Gateway — `internal/gateway/matrix/`

New gateway for Matrix (Element, etc.) via matrix-nio or raw HTTP.

### Approach: Raw HTTP/SSE against Matrix Client-Server API
- Login with `POST /_matrix/client/v3/login`
- Sync with `GET /_matrix/client/v3/sync` (long-poll)
- Send with `PUT /_matrix/client/v3/rooms/{roomId}/send/m.room.message/{txnId}`
- No heavy SDK — just HTTP calls

### Minimal viable:
- `internal/gateway/matrix/bot.go` — implements `gateway.Channel`
- Receive: sync loop with since token tracking
- Send: PUT message endpoint
- Health: check sync token is advancing

### Files:
- `internal/gateway/matrix/bot.go`
- `cmd/overkill/gateway_cmd.go` — wire matrix into gateway command

## Verification
- `go build ./...` passes
- `go test ./...` passes
- `npx tsc --noEmit` passes if TS changes exist
