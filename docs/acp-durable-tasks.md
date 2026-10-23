# Durable external ACP tasks

External ACP work can now be submitted without waiting for an agent stream. The server records a stable task/message handle in PostgreSQL, executes queued work under shared ownership locks, and retains status and reply across disconnects and server restart. This is separate from local workspace peer coordination and from scheduled daemon jobs.

## Application-owned peers

Configure named destinations in the caller's config:

```toml
[[acp.peers]]
name = "backend"
url = "https://backend.example"
token_file = "/absolute/private/backend.token"
```

A peer name fixes its URL and credential. Named tools accept neither caller URLs nor tokens. The application reads the token once when building runtime services; schemas expose names only, and receipts/errors do not copy authorization headers. The token file must be a regular non-symlink file of at most 4,096 bytes, private on Unix (for example mode 0600), with a printable non-whitespace ASCII token. Windows ACL privacy has not been accepted on this host. HTTPS is required except for explicit loopback HTTP. The adapter refuses redirects, including redirects to another local port.

These are trusted configuration and API credential boundaries, not an OS sandbox. They do not prevent an already privileged process from reading files. The receiver's existing server token authorizes its ACP API as a whole; this does not add per-peer/project credentials or authenticate the optional `from` display label.

Configured runtimes expose:

| Tool | Input beyond `peer` | Result |
| --- | --- | --- |
| `acp_sessions` | None | Existing remote session IDs; choose the intended persisted workspace session |
| `acp_submit` | `session`, `content`, `idempotency_key` | Immediate durable task receipt; queued/running means unfinished |
| `acp_status` | `task_id` | Current persisted status, partial/terminal reply, error and latest progress |
| `acp_await` | `task_id`, `wait_ms` from 0 to 25,000 | Current receipt after a bounded observation; timeout/disconnect does not cancel the work |
| `acp_cancel` | `task_id` | Recorded cancellation or the already-terminal receipt |

`acp_send` also supports `{async:true,peer,session,content,idempotency_key}` through the same configured adapter. Its existing URL/token streaming form remains a compatibility path that waits for a reply. Prefer named submission for durable work. No automatic remote-session creation occurs during a submission: creation after an uncertain response could allocate a different session or task. A session must already exist; session listing does not silently create one.

Reader mode blocks submit/cancel/send mutations. Scoped child contracts cannot use remote mutations without a determinable owned scope; observation tools remain available under the existing privilege policy. No linked external task budget/hop-limit or automatic safe-turn notification equivalence is claimed. Callers can continue independent work after submission and explicitly poll/await later.

## Protocol

`overkill acp serve` migrates task storage and starts the worker service after successfully binding its listener. Embedded/mounted servers use `Config.Tasks` and must explicitly call `StartTasks(ctx)`; constructing a Handler starts no workers.

All routes use existing ACP authentication:

- `POST /v1/tasks`: `{from?,content,sessionID,idempotencyKey}`. Returns HTTP 202 with task/message/session IDs and current state. An identical retry returns the same receipt; reuse with a different pinned body returns 409. Required keys are bounded and request objects reject duplicate, aliased, unknown, null and trailing fields.
- `GET /v1/tasks/{taskID}`: retrieve a durable receipt.
- `GET /v1/tasks/{taskID}/await?timeoutMs=0..25000`: bounded observation. Zero is an immediate poll. The initial database poll has its own ten-second limit; nonzero awaits also respect the caller's requested observation context.
- `POST /v1/tasks/{taskID}/cancel`: record cancellation. Completed/failed/interrupted tasks retain their terminal outcome.

States are `queued`, `running`, `completed`, `failed`, `cancelled` and `interrupted`. Task and message IDs are stable. Replies and task records remain after session deletion. The original session identity is immutable; a separate nullable reference observes deletion. Retrying the original key after deletion returns its original receipt, while a new request to a deleted session fails. Recreating a session with the same ID does not reconnect an old task's reference. Queued work whose session was deleted fails without running an agent.

The idempotency key is scoped to the receiver's database schema, not to `from`. Retain the exact key, session and body across an uncertain POST. Do not invent a new key merely because a response was lost. Client handle operations reject path-like IDs. A successful receipt is admission, not proof of work completion.

## Ownership, recovery and limits

Updated servers sharing a schema coordinate through PostgreSQL advisory locks held on dedicated connections. Four worker-slot locks bound concurrent durable runs across those processes. Task and session locks prevent duplicate claims and coordinate updated legacy `/v1/messages` execution with durable work. Session locks are separate from ordinary application session admission; unrelated/older/manual executors do not automatically participate.

A heartbeat checks ownership and cancellation. Lost ownership cancels cooperative execution. Queued tasks can be dispatched after restart. A prior running task is marked interrupted only after its owner lock becomes available; a live second server's work is not stolen. Interrupted work is never automatically reexecuted. An operator must inspect effects before authorizing new work.

Cancellation is recorded immediately and then cooperatively stops execution. It cannot undo effects or prove that a context-ignoring external action has stopped. Server shutdown cancels and waits for its owned workers. If cancellation breaks the ownership connection before finalization, the worker discards it, reacquires the task lock on a fresh connection and records interruption while preserving only the last durable checkpoint. It never uses this repair to claim success or replay work. Shutdown reports an error if interruption cannot be confirmed; another owner's lock is not bypassed. A broken lock connection is discarded rather than returned to the pool with advisory locks. Worker failures produce a generic runtime `workerError` status indication and prevent fresh admission while the serving worker service is unhealthy. Detailed internal failures stay out of model receipts.

Limits: 256 queued/running tasks per schema, four active durable runs, 64 KiB content, 256 KiB reply, 4 KiB error/progress, 16,384 stream events, fifteen minutes per run, ten seconds per database operation, and at most twenty-five seconds per await. Pools need capacity for worker ownership plus ordinary queries (at least three open connections). Checkpoints retain the latest bounded reply/progress, not a full replayable event ledger. Terminal receipts and keys have no automatic deletion; total historical database storage is not globally bounded.

These controls do not establish exactly-once external effects, automatic replay safety, paid-provider compatibility, per-parent opaque child capabilities, completion batching/adapter acknowledgement, nested spawn/steering, or a comprehensive credential/project isolation matrix.

## Executed acceptance

Actual PostgreSQL tests cover simultaneous idempotent submissions, changed-body conflicts, capacity, replies after restart, cancellation, server shutdown, queued recovery, live-owner preservation across servers, abandoned running interruption, real ownership-connection loss, four-run contention across servers, task/session locking, legacy compatibility, strict protocol inputs and receipt preservation after session deletion. Independent OS worker subprocesses contend for one task and record one effect.

The CLI acceptance builds the actual executable and launches frontend/backend processes with independent schemas/workspaces. The frontend model calls the named submit tool, receives a nonterminal handle, writes a real file while the backend provider is still waiting, awaits the backend reply and completes. Backend restart preserves the task/reply; the same-key retry makes no additional provider call. Provider request inspection verifies the peer token did not enter model messages. The controlled provider is local; no paid/live provider or third-party message was used.

Evidence: `.cache/acp-durable-tasks-final.jsonl`, `.cache/acp-durable-tasks-vet.log`, `.cache/acp-durable-two-process.log`, `.cache/async-adapter-final.log`. The existing DMG remains frozen and excludes this source addition.
