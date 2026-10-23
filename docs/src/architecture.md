# Architecture

Overkill's backend is a Go executable. Interactive terminal mode uses an Ink/React frontend in a Node.js process; the development desktop uses Electron with a bundled backend and private PostgreSQL process. The optional Python gRPC bridge supplies embeddings, reranking and vector operations. These are distinct components with different runtime dependencies.

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
- **Session**: PostgreSQL-backed CRUD, bound to a canonical workspace. Session-specific runtime tools are assembled with that workspace and identity.

## Storage

State is split between PostgreSQL, local files and process memory. A filesystem `sessions` setting is not a PostgreSQL data directory or a complete backup of conversations.

The configuration root is `OVERKILL_HOME` when set, otherwise `~/.overkill` on Unix and `%LOCALAPPDATA%\overkill` on Windows (with a home-directory fallback). The following paths are relative to that root in the shared CLI/API runtime:

| Data | Storage and lifetime |
|---|---|
| Sessions, messages and database-backed memory | PostgreSQL, selected by `database_url` / `DATABASE_URL`. Workspace metadata lives in the session records. |
| Peer agents, task messages/events, contracts and shared usage | PostgreSQL coordination tables. The file-backed task store below is a separate workflow subsystem. |
| Managed browser workflow records | PostgreSQL `web_task_flows`, wired by `overkill web`; these are caller-reported records, not automatically executing tasks. See [managed browser flows](../web-flows.md). |
| Goals, learning records, cost records, SOPs, cron jobs and regression cases | PostgreSQL when a database is supplied to the shared runtime. Availability must not be inferred from a tool's name alone. |
| Plans and workflow tasks | `plans/<session-id>.json` and `tasks/<task-id>.json`. |
| Tags and playbooks | `tags` (a JSONL file in the shared runtime) and `playbooks/*.json`. The standalone tag manager's fallback name is `tags.jsonl`. |
| Learned workflow files and standing orders | `learnings/`, `failed_hypotheses/`, `standing-orders.jsonl`, and other feature-specific files. |
| Context segments, flight records and checkpoints | `segments/<session-id>/`, `journal/`, and `checkpoints/`; checkpoint data also refers to the workspace. |
| Skills, hooks, configuration and personality artifacts | Local files, including `skills/`, `skill-state/`, `hooks/`, `config.toml`, `user.yaml` and `memories/`. Several older commands use literal `~/.overkill` paths; a custom configuration root does not relocate every historical artifact. |
| Gateway chat-to-session bindings | `gateway-router.json`. This stores routing metadata, not conversation history. |
| WhatsApp paired-device credentials | A SQLite database owned by the whatsmeow library, normally `~/.overkill/whatsapp.db`, or its configured `store_path`. It is separate from application PostgreSQL storage. |
| Active streams, queues and relationship beats | Process memory unless explicitly persisted by the executing entry point. Relationship snapshot/event-log helpers exist, but the CLI's beat tracker is not wired to load/save them automatically. Cold-start personality files do not prove that all relationship beats survive restart. |

Standalone CLI mode can run without a database; that does not provide durable conversation history or database-backed coordination. An invalid configured database is an error, not a fallback to a local conversation file. The API-backed interactive path needs its configured PostgreSQL service; see [installation](install.md).

The desktop sets its backend configuration root to `<Electron userData>/backend` and owns a private cluster at `<Electron userData>/backend/postgres`. Its `settings.json` lives in the Electron profile and stores the provider credential encrypted through Electron's OS-backed `safeStorage`. Ordinary workflow files are not thereby encrypted. See the [desktop guide](../../desktop/README.md) for lifecycle and packaging details.

The Python bridge defaults to the **PostgreSQL** vector backend. It requires PostgreSQL with pgvector and `DATABASE_URL` (or `PG_URL`) for persistence. Explicitly selecting `OVERKILL_MEMORY_BACKEND=inmem`, or a request's `inmem` backend, uses disposable process memory; it does not silently replace failed PostgreSQL storage. Real semantic embeddings/reranking additionally need the bridge's `[ml]` dependencies and loadable models. See the [bridge guide](../../bridge/README.md).

Back up the database **and** the relevant configuration/workflow files and workspaces. Preserve paired-device credentials separately when using whatsmeow. A database dump alone does not include these local files, and copying a live PostgreSQL cluster directory is not a substitute for a supported database backup.

## Availability and experimental surfaces

- The desktop is a development workspace. Its Tasks, Contracts and Checks panels expose backend records; a complete editor/terminal workspace, full task graph and all review/notification workflows remain unfinished. An older DMG does not automatically include current source changes.
- The unified `overkill gateway` command currently reports configuration rather than launching all bots. Platform commands, the daemon and the API bridge have different wiring; consult the [gateway matrix](../gateways.md) before choosing an entry point. Desktop startup intentionally disables gateways and automatic sync push.
- Extension inventories reflect actual registered skills, trusted hooks and live MCP state. Plugin discovery does not execute plugins in the shared runtime; MCP per-server runtime toggling remains unsupported. See [extension runtime behavior](../extensions-runtime.md).
- `self.eval.status` reports actual checks and heuristic confidence separately. The full iterative self-evaluation loop has no production caller and is unavailable; confidence is not a passing test.
- Personality response-style instructions are wired into provider requests. Persistent relationship-beat recovery, automatic self-modification and every personality subsystem are not established merely by the presence of their packages.

These notes describe source wiring, not successful live authentication or delivery on each external platform.
