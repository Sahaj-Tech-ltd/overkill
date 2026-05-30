# AGENTS.md

Instructions for AI coding assistants working on the Overkill codebase.

## Build & Run

```bash
go build ./...
go build -o bin/overkill ./cmd/overkill
go test ./...
go test -race ./...
```

## Lint

```bash
golangci-lint run
golangci-lint run --fix
ruff check bridge/
ruff format bridge/
```

## Architecture

```
cmd/overkill/       CLI entrypoint (Cobra)
internal/           Private packages (not importable externally)
  agent/            Core ReAct loop: think → act → observe
  config/           TOML config loading, validation, auto-migration
  security/         Prompt injection detection, command scanning, path blocking
  compaction/       LCM-inspired context compaction (dual-state memory)
  routing/          Complexity-based model routing + pricing-aware fallback
  session/          Per-folder sessions backed by Postgres
  tools/            Built-in tools: shell, fs, grep, git, web
  providers/        LLM adapters: OpenAI, Anthropic, Gemini, Ollama, etc
  tokenizer/        Token counting and estimation
  cost/             Token/cost tracking, budget enforcement
  hooks/            Lifecycle hooks (before/after tool exec, session events)
  skills/           Skill loading, registry, SKILL.md parsing
  memory/           Memory orchestration (Go side, delegates to bridge for vectors)
  cron/             Timezone-aware cron scheduler
  doctor/           Auto-heal broken configs, environment checks
  automation/       SOP engine, routines, alarm clocks
  personality/      Personality engine, relationship tracking, soul.md
  rewriter/         Prompt rewriter middleware (anti-bloat, sycophancy reduction)
  pipeline/         Incremental dev: spec → test → code → refactor
  walls/            3 Walls quality gates
  diagnostic/       Debugging diagnostic report generation
  introspection/    On-demand self-knowledge skill (NOT read on boot)
  journal/          Flight recorder, journal sub-agent, alert system
pkg/
  api/              Public Go API
  tui/              Terminal UI (Bubble Tea + Lip Gloss)
bridge/             Python bridge via gRPC
  embeddings/       Embedding generation
  reranking/        Result reranking
  memory/           Vector memory backends (Postgres default, Qdrant optional)
  compaction/       LLM-based compaction via cheap model
  proto/            gRPC proto definitions
skills/             Bundled skills (SKILL.md format)
```

## Key Interfaces

### Provider (internal/providers/)
```go
type Provider interface {
    Complete(ctx context.Context, req Request) (Response, error)
    Stream(ctx context.Context, req Request) (<-chan Chunk, error)
    Models() []Model
}
```

### Tool (internal/tools/)
```go
type Tool interface {
    Name() string
    Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}
```

### Session (internal/session/)
```go
type Store interface {
    Create(ctx context.Context, session *Session) error
    Load(ctx context.Context, id string) (*Session, error)
    Save(ctx context.Context, session *Session) error
    List(ctx context.Context, folder string) ([]Session, error)
}
```

## Storage

All local storage uses **PostgreSQL**. No SQLite, no embedded KV stores (exception: WhatsApp/whatsmeow requires SQLite via `modernc.org/sqlite` — third-party library requirement, not first-party storage).

## Config

Config format is **TOML**. Config dir is `~/.overkill/`. Config auto-migrates on version bumps.

## Conventions

- Go: `gofmt`, `golangci-lint`, wrap errors with context
- Python: `ruff`, type hints on public functions
- No panic in library code
- Interfaces defined at consumer, not implementation
- Conventional Commits enforced
- Each PR addresses one concern

## Testing

```bash
go test ./...                          # All tests
go test -race ./...                    # With race detector
go test -run TestAgent ./internal/agent/  # Specific package
go test -cover ./internal/...          # Coverage
```

## Python Bridge

```bash
cd bridge
pip install -e ".[dev]"
pytest
ruff check .
```

## Inspiration Lookups (DeepWiki)

When asked "how does X work in Claude Code / OpenCode / Hermes / ZeroClaw / OpenClaw / OpenClaude / ClawCode", use DeepWiki to get a full codebase map BEFORE diving into local clones. DeepWiki indexes every function and maps natural language concepts to code entities.

**DeepWiki URLs:**
| Project | DeepWiki | Local Clone |
|---|---|---|
| OpenClaw | `https://deepwiki.com/openclaw/openclaw` | `inspiration/openclaw/` |
| OpenClaude (OSS Claude Code) | `https://deepwiki.com/Gitlawb/openclaude` | `inspiration/openclaude/` |
| Hermes Agent | `https://deepwiki.com/NousResearch/hermes-agent` | `inspiration/hermes-agent/` |
| ZeroClaw | `https://deepwiki.com/zeroclaw-labs/zeroclaw` | `inspiration/zeroclaw/` |
| OpenCode | `https://deepwiki.com/anomalyco/opencode` | `inspiration/opencode/` |
| Pi | `https://deepwiki.com/earendil-works/pi` | `inspiration/pi/` |
| Warp | `https://deepwiki.com/warpdotdev/warp` | `inspiration/warp/` |
| Claw Code | `https://deepwiki.com/ultraworkers/claw-code` | N/A |

**Procedure:**
1. `webfetch https://deepwiki.com/<org>/<repo>` (markdown format) — get the architecture overview
2. Drill into specific headings as needed (e.g., `/Gitlawb/openclaude/4-tool-system`)
3. Cross-reference against local clones in `inspiration/` for implementation details

DeepWiki pages are fully indexed and map every function to its source location. Always use DeepWiki first — it's faster than grep across a 365k-star repo.

## Answering "Where Is" Questions

When the user asks where a file or directory is:

1. **Give the full system path** — `/home/user/overkill/internal/agent/loop.go`, not `internal/agent/loop.go`. User should be able to copy-paste and `cd` or `cat` directly.

2. **If the user is on a channel (Telegram, Discord, etc.)** — `cat` the whole file and show it. Channel users can't browse the filesystem. Don't make them ask twice. Say "looks good?" so they can confirm or ask for edits. If the file is too long for one message, chunk it.

3. **If the file is a design doc / spec** — summarize the structure (sections, line count, what it covers) first, then offer to dive into specific sections.
