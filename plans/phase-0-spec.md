# Phase 0: JSON-RPC Backend API

## Goal
Create a JSON-RPC 2.0 HTTP server at `/home/harsh/docker/overkill/internal/api/` that wraps the existing Go backend (internal packages) behind a clean API. The Ink TUI will consume this in Phase 1.

## Architecture
```
Ink TUI (Phase 1) → HTTP JSON-RPC → internal/api/server.go → existing internal packages
```

## Files to Create

### 1. `internal/api/types.go`
Shared request/response types:
```go
package api

// JSON-RPC 2.0 envelope
type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      int             `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
    JSONRPC string      `json:"jsonrpc"`
    ID      int         `json:"id"`
    Result  interface{} `json:"result,omitempty"`
    Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}
```

Define typed params/result structs for each method:
- SendMessageParams, SendMessageResult (streaming over SSE)
- SessionInfo, SessionListResult
- ConfigInfo
- ProviderInfo, ModelInfo

### 2. `internal/api/handlers.go`
Handler functions that call into existing internal packages:

Methods to implement:
- `agent.send` — takes message string, returns streaming SSE response. Uses `internal/agent` package.
- `agent.abort` — cancels current agent run
- `session.list` — returns []SessionInfo from `internal/session`
- `session.create` — creates new session
- `session.delete` — deletes session by ID  
- `config.get` — returns current config from `internal/config`
- `config.update` — patches config
- `providers.list` — returns configured providers from `internal/providers`
- `models.list` — returns models for a provider
- `status.health` — health check

### 3. `internal/api/server.go`
HTTP server that:
- Listens on `localhost:0` (random port — prints chosen port to stderr)
- Exposes `POST /rpc` endpoint for JSON-RPC
- Exposes `GET /health` for health check
- Exposes `GET /sse?session=X` for streaming agent responses (Server-Sent Events)
- CORS headers for localhost development
- Graceful shutdown on SIGINT/SIGTERM

### 4. `internal/api/middleware.go` (optional)
- Request logging
- Panic recovery
- CORS middleware

## Constraints
- **Do NOT modify any existing internal packages** (agent, config, session, providers, etc.)
- **Do NOT touch `pkg/tui/` or `cmd/overkill/tui.go`** — those stay until Phase 5
- **Do NOT change BadgerDB** — use the existing session store as-is
- **Keep it minimal** — only expose what the Ink TUI actually needs, not everything
- **Use only Go stdlib** for HTTP (net/http) — no new dependencies unless needed
- **Wire up real internal packages** — don't stub with TODO placeholders. Actually call session.List(), config.Load(), providers.List(), etc.
- **The agent.send handler must actually work** — import `internal/agent` and call the real agent loop. Pass a context for cancellation.

## Verification
After creating these files, do NOT `go build` or `go test` unless you're fixing compilation errors. The files should compile cleanly against the existing codebase.

## Key Context
- Module: `github.com/Sahaj-Tech-ltd/overkill`
- Go version: whatever is in go.mod
- Config: `~/.overkill/config.toml` (loaded by `internal/config`)
- Session store: `internal/session` backed by BadgerDB at `~/.overkill/sessions/`
- Agent: `internal/agent` package with `Agent` struct

## Tips for Implementation
- Read existing code before writing — look at how `cmd/overkill/tui.go` calls `session.NewStore()`, `config.Load()`, etc.
- The agent.Send method probably takes context, message, session, and a streaming callback
- For SSE streaming: use `http.Flusher` to push chunks
- Port: use `:0` to let OS pick, print the actual port to stderr with `log.Printf("API listening on :%d", port)`
