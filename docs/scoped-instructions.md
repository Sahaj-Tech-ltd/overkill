# Scoped workspace instructions

Overkill can load `AGENTS.md` into the actual provider prompt for standalone `run`, API sessions (including the desktop/TUI API path), and delegated/audit children. This is disabled by default. Enable it only for workspace guidance you authorize the model to follow:

```toml
[agent.instructions]
enabled = true
# Optional explicit absolute path; no implicit home-directory discovery.
global_file = "/Users/me/.config/overkill/AGENTS.md"
# Optional existing directory relative to each session's workspace.
directory = "backend/service"
```

The session workspace is the project boundary. No filesystem-root or repository-marker search occurs. Loading order is the explicit global file, workspace-root `AGENTS.md`, then each `AGENTS.md` down to the configured directory. Later, more specific files take precedence within their directory scope. Explicit user instructions and system safety requirements retain precedence. Sibling directories and ancestors outside the workspace are excluded. Only the exact `AGENTS.md` filename is supported; aliases and override filenames are not inferred.

`directory` selects guidance; it does not change filesystem tools' working paths. Directory selection is explicit: tools reading a file do not automatically activate its ancestor instructions. API callers embedding the agent may call `Source.SetDirectory(relativePath)` to select a different contained directory between turns. Invalid selections return an error and keep the previous directory. Each child uses its own workspace and instruction source, built from the same application configuration; a child does not inherit its parent's transcript or copy of instruction contents.

Enabling this feature authorizes the configured global file and workspace instruction files as model guidance. Project paths and symlink targets must resolve inside the workspace; a configured global file is an explicit exception. Invalid initial workspace/directory configuration rejects agent construction. Missing instruction files are normal. Unreadable, non-regular, invalid UTF-8 and oversized files are omitted in their entirety, with a diagnostic in the prompt. Limits are 32 KiB per file and 64 KiB total raw instruction content; later files exceeding the total are omitted with a diagnostic. Canonical paths are deduplicated. JSON serialization separates file paths/content from surrounding prompt structure. These checks do not provide an execution sandbox or protection against an adversary concurrently replacing host filesystem entries.

Files are reread for each provider request. Edits and deletions affect the next request; there is no persisted instruction-content cache. Instructions are appended after lossy prompt compression and included in the budget estimator. Conversation compaction and history restore therefore do not discard the current instruction source. Agent embeddings revoke guidance with `SetInstructionSource(nil)`. Changing TOML enablement, paths or directory requires constructing/restarting the session with the new configuration; this feature does not add config hot reload. Revocation removes the instruction block from future requests, not facts an assistant may already have repeated into the conversation.

Existing independent agent constructors used by legacy `web` and messaging gateways are outside this adoption. No directory-selection UI, automatic tool-read discovery, project trust approval UI, branch-selection implementation, memory retention/deletion feature, or pgvector retrieval behavior is introduced here.

## Desktop preference

Desktop Settings includes **Use workspace AGENTS.md**, off by default. Enabling it authorizes the selected workspace's root instruction file and saves the setting for backend reconnection. The desktop preference does not select nested directories or a global file; those remain explicit configuration/embedding controls described above. Native source acceptance checks that an actual provider request contains a marker from the selected workspace file.

## Evidence and bounded adoption

The prior implementation read root instruction filenames in `internal/agent/plan_agent.go` for project analysis, while `buildRequest` assembled base prompt, skills, memory, personality and host context without scoped file discovery. Bootstrap's home `CLAUDE.md` creation was likewise not ordinary request-time instruction loading.

The checked-in research snapshots informed this change:

| Harness | Pinned evidence | Relevant contract and difference |
| --- | --- | --- |
| Codex | `research/harnesses/codex/source-metadata.json`, commit `0769c6aeeb1a81cec715e496d2486ece6fcef21b`; `source/codex-rs/core/src/agents_md.rs` | Root-to-current-directory collection, configured root markers, override/fallback files, trust gating and byte budget. Overkill uses an explicit workspace boundary and opt-in authorization, without Codex's full discovery/trust model. |
| OpenCode | `research/harnesses/opencode/source-metadata.json`, commit `1b1ad488f833e37af57d903d995a3bdbef752756`; `source/packages/core/src/session/instructions.ts` | Nearby instructions become synthetic session messages with a visible-history dedup ledger and reinjection after compaction/revert. Overkill instead reloads guidance directly into each request and does not implement read-triggered discovery or that ledger. |
| Pi | `research/harnesses/pi/source-metadata.json`, commit `58c0b02ce552adab76014e7ed712ab7981359aa8`; `source/packages/coding-agent/src/core/resource-loader.ts` | Agent-directory global context plus ancestor context, filename candidates and linked-worktree shadow handling. Overkill intentionally does not traverse outside the authorized workspace or adopt these filename/worktree rules. |
| Factory | `research/harnesses/factory/wiki/official/harness/agents-md.md`, captured official documentation | Documents durable root and nested project guidance. This is documentation evidence, not an inspected Factory implementation or executed upstream runtime. |

This is a bounded A03 instruction-loading adoption. It does not establish full A03 memory/retention acceptance or runtime parity with any upstream harness. The existing global research catalogues are not promoted by this change.

## Verification

The tests capture actual provider requests, including the standalone CLI process, production API agent creation with Run/Stream and resume, and production child factory. They check ordered global/project/directory guidance, next-request edits and deletions, scope changes, explicit revocation, budget accounting, actual conversation compaction, retention after prompt compression, disabled configuration, whole-file size/UTF-8 errors, total budget errors, and outside-workspace symlink rejection. The fixtures test prompt assembly, not whether a remote model obeys every instruction.

```sh
.cache/toolchains/go/bin/go test -race ./internal/instructions ./internal/agent ./internal/childruntime ./internal/api ./internal/config ./cmd/overkill -run 'TestScopedInstructions|TestSource|TestChildInstructions|TestAPIScopedInstructions|TestStandaloneCLIScopedInstructions' -count=1
```
