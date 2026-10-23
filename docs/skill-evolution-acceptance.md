# Project skill evolution: engine acceptance

This note covers `internal/learning`. Agent recording, authenticated operator RPC, desktop controls, provider/session wiring and actual installed-app acceptance are owned by the application integration lane and require their own evidence.

## Runtime contract

`NewEngineWithOptions(recordDir, skillDir, clusterer, generator, judge, EngineOptions{Workspace, KnownSecrets, MaxRecords, Retention})` binds an engine to a canonical workspace hash. The application supplies resolved configured credential values only for redaction; these values are not serialized. Separate private runtime directories per workspace provide storage separation too.

`RecordTurn(ctx, LearningRecord)` performs local bounded recording only. It preserves a task hash, workspace hash, bounded final output, session/model identifiers and tool names/errors. Raw arguments, tool results, attempt trails and late guidance are removed, without mutating the live input slices. Known credentials are checked before truncation; secret/injection findings replace the whole field. Heuristic scans cannot prove the absence of every unknown or encoded secret. Callers should supply their full configured redaction inventory and avoid collecting sensitive content unnecessarily.

The spool is private (directory 0700, file 0600), rewritten atomically, and limited to 512 newest records, 4 MiB total, 16 KiB per record and 30 days. Configured smaller retention/count limits are honored. Reading a historical oversized spool considers only its bounded tail and does not accumulate every newline/record in memory. A subsequent record rewrites a sanitized bounded spool. A scanner failure rejects/redacts content rather than passing it through.

`RunColdPath(ctx, workspace, false)` reads matching workspace records, clusters them, and generates at most eight proposals. A missing explicit workspace, mismatched workspace, or legacy `applyMode=true` fails closed. Generation never installs skills. Candidate drafts are persisted; unsafe or malformed drafts are quarantined with their unsafe body and metadata removed. The bounded repository permits at most 256 stored draft files; reaching capacity fails explicitly rather than silently discarding review/audit state. Operator archival/removal of old drafts is not currently exposed by the engine API.

`NewProviderDraftGenerator(ProviderDraftOptions{Provider, Model, MaxTokens, Timeout, BudgetGate, RecordUsage})` uses the actual configured provider interface. Every request requires budget admission and usage recording; it has no tools, caps output at 2048 tokens and a 30-second timeout, and accepts one strict JSON content proposal. Null/empty/unknown approval fields, unsupported change kinds and invalid target names are rejected. Known usage is recorded even when the returned proposal is malformed or a provider request returns an error. An omitted/null usage object with all-zero counters is rejected before accounting or proposal publication; it is not written as a fictitious free call. Explicitly reported zero usage and legacy adapters providing nonzero counters remain supported. These checks bound individual calls and admit each request; they are not an invoice reservation across concurrent sessions.

## Explicit review, installation and rollback

`ListDrafts(ctx)` returns persisted proposals for the engine's workspace. `ReviewDraft(ctx, id, approve)` is a trusted operator API, deliberately absent from model tools. Approval captures the existing skill's content hash. `ApplyReviewedDraft(ctx, id)` rechecks approval/security and refuses a target changed since review. It creates an immutable checked backup before replacing an existing skill. Create, replace and append have distinct behavior; arbitrary patch/merge execution is unsupported.

File operations use validated single-component skill names and `os.Root`; escaping or final symlinks and non-regular skill files are rejected. Skill files and backups are bounded at 64 KiB. A failed backup aborts before modifying the skill. Per-store operating-system locks serialize independent engines/processes, and atomic private writes avoid shared predictable temporary filenames.

Apply records an `applying` intent before changing content. Repeating the operation after interruption recognizes the intended complete hash and finalizes the same installation without appending twice. Rollback similarly records `rolling_back`, restores the checked backup (or removes a newly created file), and refuses to overwrite later edits. Persisted interrupted status permits recovery if the skill write succeeds but the final status write fails. A separate `applied` status is required for context activation.

`LoadAppliedGuidance(ctx) ([]AppliedGuidance, error)` returns `{ID, Name, Body, AppliedHash}` only when the draft is APPLIED, reviewed/applied timestamps exist, its workspace matches, the current complete SKILL.md hash matches the installed hash, and a fresh security scan passes. Candidate, approved-but-uninstalled, quarantined, rejected, interrupted, rolled-back, edited or other-workspace content is omitted. The result is capped at eight items and 32 KiB total; oversized skills are omitted rather than partially activated. The application loads this guidance subordinate to current user requests and tool permissions.

## Verification

- `.cache/evolution-usage-presence-final.jsonl`: final focused race gate after the usage-presence fix, **31 named tests/subtests passed, zero skips/failures**, including actual HTTP omitted/null/explicit-zero/nonzero responses and legacy nonzero adapters.

- `.cache/evolution-learning-full.jsonl`: full learning package with required real PostgreSQL, race detector, **90 named tests/subtests passed, zero skips/failures**.
- `.cache/evolution-acceptance.jsonl`: focused evolution gate, **26 named tests/subtests passed, zero skips/failures**, before the final immutable compiled-scanner performance adjustment; the subsequent full gate covered the pre-usage-presence checkpoint; the final focused gate above verifies the later generator-only accounting change.
- `.cache/evolution-lock-fixed.log`: independent engines and 100 simultaneous descriptor creations, repeated five times with the race detector, passed.
- `.cache/evolution-vet.log` and `.cache/evolution-windows-build.log`: vet and Windows test-binary cross-compilation passed. Cross-compilation does not establish Windows runtime acceptance.

The provider acceptance test runs a real OpenAI-compatible local HTTP server, captures the request, verifies sanitized evidence/model/output cap, records reported usage, persists a candidate, restarts the engine, explicitly reviews/applies, and loads hash-verified guidance. Other tests exercise private permissions, retention, legacy oversized-file repair, secrets/injection, traversal and symlink sentinels, backup failure, concurrent idempotent apply, later-edit conflicts, interrupted apply/rollback recovery, workspace separation and guidance bounds. No paid provider or host Keychain calls are made.

The original concurrent-open failure is retained in `.cache/evolution-lock-debug.log`: on this macOS, simultaneous `os.Root.OpenFile(O_CREATE)` calls could return ENOENT while the named file already existed. Exclusive creation plus bounded existing-file retries and descriptor/name identity checks resolves the reproduced case without bypassing `os.Root` containment. Historical failing logs remain evidence of the repaired issue, not current acceptance results.
