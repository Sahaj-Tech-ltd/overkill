# Per-item execution context acceptance

The shared Agent now schedules bounded independent work items for both Run and Stream. Each item has a fresh model conversation, its own durable compaction namespace, and selected source evidence read from the configured workspace through a pinned `os.Root`. The outer admitted Agent retains its tools, permissions, goal accounting, cancellation and session identity. Successful terminal results contain every item's answer, so the desktop's final response replacement preserves reports.

Source selection prefers explicit relative paths with token boundaries over keyword matches. A bounded deterministic workspace index excludes dependency/build trees, credential filenames and common binaries. Source reads skip symlinks, binary data and files flagged by the existing secret scanner. Limits are 10,000 index entries, 4,096 files indexed, eight selected files, 16 KiB per file and 64 KiB aggregate, further clamped to the current model budget. Source prefixes can be truncated. Source text is a separate evidence message; embedded at-mentions do not cause additional automatic reads. Ambient git/context-provider text and automatic persistent-memory prompt injection are suppressed during items. Explicit instructions, skills, approved evolution guidance, personality and standing goals remain applicable. Normal explicitly requested tool operations retain existing authority.

The durable session transcript retains prior conversation and all raw item messages, including tool calls/results, while the active model sees only its item. Compaction archives raw messages before replacing the active view. PostgreSQL checkpoints persist the current item, opaque compaction scope, completed transcript, raw active transcript and compacted active view. Resume uses the same admission and authority checks and continues after completed tool actions rather than replaying earlier items. A queue checkpoint is not emitted as premature successful completion.

## Verification

- `.cache/sequential-focused-final.jsonl`: race-enabled actual OpenAI HTTP Complete/SSE fixtures, real PostgreSQL resume and goal usage, workspace/source isolation, exact-path selection, secret/dependency/symlink exclusion, Reader authority, concurrent admission, cancellation and corrupt-cursor rejection. Final response assertions preserve all three item bodies.
- `.cache/sequential-scope-final.jsonl`: real PostgreSQL and actual summary-provider HTTP fixture prove scoped DAG persistence, isolation from base/other-item/other-session namespaces, and exact committed retry reuse after rebuilding the compactor.
- `.cache/sequential-broad-final.jsonl`: broader race regression gate; consult receipt for final status.

These fixture providers make no paid model calls. Installed-desktop evidence is recorded separately by the native acceptance lane.

## Limits

Automatic relevance is lexical, not semantic indexing; callers can name files explicitly. A queue accepts two to ten items, 64 KiB total input and 16 KiB per item. Per-item execution has a five-minute bound within the existing outer budget. Ambiguous attachments on a multi-item Stream request are rejected. Durable checkpoint payloads are bounded to 8 MiB; invalid/unfinished tool groups are rejected. Scope data is retained without automatic garbage collection. The pinned source reader is not an operating-system sandbox for explicitly authorized tools. This does not claim every policy document roadmap feature is implemented.
