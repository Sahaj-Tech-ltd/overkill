# Architecture

Overkill is a single Go binary with an Ink (React) terminal UI running in a sidecar Node.js process.

## High-level

```
┌─────────────────────────────────────────┐
│  cmd/overkill/  — CLI entrypoint (Cobra) │
├─────────────────────────────────────────┤
│  internal/agent/    — ReAct loop        │
│  internal/providers/— LLM adapters      │
│  internal/tools/    — Built-in tools     │
│  internal/session/  — Postgres store     │
│  internal/config/   — TOML config       │
│  internal/compaction/— Context mgmt     │
│  internal/security/  — Injection scan   │
│  internal/cron/     — Scheduler         │
│  internal/doctor/   — Health checks     │
├─────────────────────────────────────────┤
│  tui/               — Ink/React TUI     │
│  bridge/            — Python gRPC bridge │
├─────────────────────────────────────────┤
│  Postgres           — Session + memory   │
│  MCP servers        — Optional tools     │
│  LSP servers        — Optional code intel│
└─────────────────────────────────────────┘
```

## Core loop

```
User prompt → ReAct loop
  ├─ Think: classify complexity, select model, build messages
  ├─ Act: dispatch tool calls (shell, fs, git, web, etc.)
  ├─ Observe: capture output, check for errors
  └─ Repeat until done or max iterations
```

## Key interfaces

- **Provider**: `Complete(ctx, req) → (Response, error)` + `Stream(ctx, req) → (<-chan Chunk, error)`
- **Tool**: `Name() string`, `Execute(ctx, input) → (output, error)`
- **Session**: Postgres-backed CRUD with per-folder isolation

## Storage

All state lives in PostgreSQL under `~/.overkill/sessions/` keyed by working directory. No embedded KV stores.
