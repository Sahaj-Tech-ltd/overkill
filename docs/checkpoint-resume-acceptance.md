# Automatic checkpoints and durable continuation

This records the implementation and the executable evidence for the checkpoint/resume lane of the ZIP gap remediation. It does not certify the rest of that remediation or the entire application.

## Production path

`newRuntimeServices` creates a session-owned filesystem checkpoint manager and a shared PostgreSQL `FlowQueue`. `SessionRuntime` carries the snapshotter and a session-bound flow store into both API-created agents and the CLI agent. The desktop runs the same API server. The daemon starts that same server's background runtime without opening an HTTP listener; it no longer resumes a task with an unrelated cheap model and an empty tool registry.

Filesystem recovery is installed automatically; the user need not persuade the model to invoke a snapshot tool. Actual `fs` writes/edits, patch calls and shell execution capture before mutation. Read-only mode creates no checkpoint. A failed enabled capture prevents tool execution. Parallel mutations in one agent serialize capture and execution. Ordinary Git history and the user's index are not changed by automatic capture.

Successful captures expose their ID, scope, file count and scope notice through the stream status and `checkpoint_created` observation. `checkpoint_list` returns durable manifests; `checkpoint_restore` restores an owning session's capture. The legacy Git slash-command manager is not automatically enabled.

## Filesystem contract

- Explicit file mutations capture the selected paths, including explicitly selected ignored files.
- Opaque shell calls capture Git-visible files: tracked and untracked nonignored files. The receipt expressly excludes ignored files and Git metadata. A nonrepository uses a regular-file workspace inventory, excluding Git metadata and checkpoint storage itself.
- The current fixed limits are 4,096 paths, 1 MiB per file and 64 MiB per capture. Exceeding a limit fails the capture; data is not silently skipped.
- Session identity and selected workspace remain fixed. `os.Root` confines actual file operations, including races involving symlink ancestors. Symlinks and special files are rejected; Unix opens are nonblocking so a FIFO replacement cannot stall file admission.
- Restore validates every path, manifest ID and content hash before changing any target. Original file permission bits are retained. A pre-restore checkpoint preserves the current bytes before overwrite/removal, allowing undo of a rollback and recovery from a later I/O failure.
- Files absent before a selected-file capture are removed on restore. Whole-inventory restore also removes newly created files in that same inventory scope. Ignored files stay outside a Git-visible inventory.
- File writes use synced temporary files and atomic rename. Manifest publication also syncs directories on supported Unix hosts. Windows retains atomic file publication without a directory-fsync guarantee.

These are file recovery operations, not a filesystem transaction or an OS sandbox. They do not restore external service effects, Git metadata, ownership/ACLs, empty-directory metadata, ignored files outside explicit selections, or concurrent external-editor changes. Automatic retention uses the configured five-per-session manager; pre-restore recovery captures remain until subsequent ordinary snapshot pruning. Broader configurable inventory/size/storage policy remains a separate gap.

## Durable continuation contract

A max-step exit or an actual deadline can create a queued continuation. Explicit cancellation and emergency stop produce paused work, not a timer retry. Missing, corrupt, unbalanced or potentially interrupted tool execution requires review. Exhausted work reports that the automatic resume limit has been reached; it does not promise another wake.

The session-bound store atomically commits the completed transcript, pinned workspace/provider/model, checkpoint and next wake in PostgreSQL before a worker can observe the wake. Jobs use a stable flow identity and monotonically increasing generation. A new checkpoint supersedes its previous wake; an old continuation cannot delete a newer checkpoint when it finishes.

The API/daemon worker uses the canonical session factory and current tool/security policy. It owns both ordinary session admission and connection-held database advisory locks across execution and persistence. Another application process cannot run the same session or flow simultaneously. The original user input and completed tool calls are not appended/re-executed as a new turn. The worker restores the full checkpoint history and continues with the same owning workspace and pinned provider/model.

A durable stop epoch fences late checkpoint publication after `agent.abort`, `estop`, or a newer human turn, including a slash command that does not append history. Conversation, workspace, provider/model or active-session changes cancel stale pending work. Local cancellation happens before durable stop confirmation, so unavailable PostgreSQL cannot prevent cancellation of an already running provider. If durable confirmation fails, the API reports that failure honestly; an in-process pause prevents local automatic wakeups. Cross-process/restart confirmation requires the durable store to be available. Desktop emergency-stop persistence remains owned by its existing stop marker and backend/process shutdown path.

Pending queued work survives backend restart and resumes automatically. A lost owner that was already executing is marked `manual_review`; the system cannot prove that an arbitrary external tool had no effects before a crash. It therefore does not claim exactly-once external side effects. Default wake delay is five minutes; there are four bounded workers and a three-continuation cap.

Loaded plans persist scheduler data for the active or not-yet-started phase, accepted earlier phases, pending later phases, and evaluation thresholds/caps. A continuation restores the scheduler without loading/replaying the plan command. It retains current tool authority and can require the saved evaluation policy without enabling auto mode. It appends a phase prompt only when that phase has not started. Callback functions, credentials and approval capabilities are not serialized.

`flow.list` accepts only `{ "session_id": "..." }` and returns `{ "enabled": true, "flows": [...] }`. Each job contains `id`, `session_id`, `status`, `due_at`, `detail`, and `generation`. It accepts no caller-supplied history or executable runtime configuration. Statuses include `queued`, `running`, `completed`, `paused`, `exhausted`, `manual_review`, `cancelled` and `failed`.

## Executed evidence

The receipt in `.cache/checkpoint-resume-acceptance.json` records exact commands, source hashes and final results. Source and tests remain in the repository; cache paths are local execution evidence.

| Evidence | What actually ran |
|---|---|
| `.cache/checkpoint-security-focused.log` | Race tests for real filesystem capture/restore, symlink containment, content corruption preflight, executable modes, absent-file removal, whole-workspace creations/deletions, recovery preservation and Git index isolation. |
| `.cache/checkpoint-runtime-acceptance.log` | Shared production runtime, actual `Run` and `Stream`, real file mutations and automatic pre-write capture, restore to original bytes/mode, cross-session restore denial. |
| `.cache/checkpoint-api-focused.log` | Real controlled OpenAI HTTP fixture through API-created agents; factory snapshot hook installed automatically; capture failure prevents the actual file write. |
| `.cache/flow-api-restart.log` | Actual listening HTTP backend and PostgreSQL; backend restart between max-step checkpoint and continuation; full tool registry writes both real files exactly once; one original user input; completed transcript persists. |
| `.cache/flow-plan-resume.log` | Real PostgreSQL and fresh agent runtimes across active/pending loaded-plan phase boundaries; actual file writes; completed phases retained; safe tool authority and evaluation requirements retained. |
| `.cache/flow-api-acceptance.log` | Actual HTTP restart test plus stale transcript/workspace/model/provider/archive rejection and real provider cancellation with the database deliberately closed. |
| `.cache/flow-process-death.log` | A real child process holds the PostgreSQL lease; another process cannot claim it; an actual process kill releases the connection and yields manual review without an additional resume/tool replay. |
| `.cache/flow-durable-race.log` | Combined final race gate: checkpoint security, durable queue/lease/generation/stop fencing, corruption, resume exhaustion, Run/Stream deadline-vs-cancel distinction, nonstreaming estop, loaded-plan continuation, overlap/corrupt-history protection, and compatibility socket/alarm tests. |

Root-owned native desktop receipts and final application/build gates are recorded separately. This document does not substitute package tests for a native-app claim, or a controlled local provider for live account/provider validation.

The broader affected-package attempt in `.cache/checkpoint-resume-full-packages.jsonl` encountered a concurrent learning-lane compile error (`persistDraft` was not yet defined). It is retained as a failed integration attempt, not a green gate. Final whole-worktree verification awaits all integration lanes freezing.
