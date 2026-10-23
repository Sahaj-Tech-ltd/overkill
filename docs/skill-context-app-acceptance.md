# Skill evolution and per-item context: application acceptance

Implemented in the shared CLI/API runtime and exercised through the rebuilt development desktop app on macOS arm64. This is source/development-app acceptance; the previously distributed DMG has not been replaced.

## Use the features

**Project skills:** completed successful agent turns produce bounded, redacted local records. Open **Project skills** in a chat and choose **Generate drafts** after at least three turns share a tool pattern. Generation uses that chat's configured provider/model, budget admission and durable usage accounting. Read the proposed content and its create/replace/append operation, approve the proposal, then separately apply it. Only applied, workspace-matching, hash-verified content enters subsequent model requests. Applied guidance survives restart; **Roll back skill** removes or restores it. Generation is explicit; recording a turn does not make a background paid provider call or install guidance.

**Per-item context:** turn on `/think`, then submit two to ten work items as bullets or a numbered list. Naming files explicitly gives the most predictable selection. Run and Stream use fresh item history and bounded actual file bytes from the selected workspace. Prior item results stay in the durable transcript, without entering the next item's model history. The final response includes every item's report. Checkpointed queues resume the active item without replaying completed work.

## Application evidence

- [Operator HTTP/PostgreSQL acceptance](/Users/abc/Documents/overkill/.cache/evolution-operator-acceptance.log): real Run/Stream capture, candidate inactivity, provider generation, separate review/apply, backend recreation, cross-workspace rejection, rollback, known-model dollar/token accounting and unpriced-model rejection under dollar caps.
- [Capture regression](/Users/abc/Documents/overkill/.cache/evolution-capture-acceptance.log): parallel errors follow tool-call IDs, including wrapped external-tool failures; raw arguments/results and configured secrets are excluded.
- [Four native desktop scenarios](/Users/abc/Documents/overkill/.cache/skill-context-native-focused.log): actual Electron, prepared Go backend, private PostgreSQL, controlled HTTP/SSE provider and full app restart. All four passed without skips: skill evolution, per-item context, checkpoint/restore, durable continuation.
- [Evolution desktop receipt](/Users/abc/Documents/overkill/.cache/evolution-native/receipt.json): three recorded turns create a proposal; an unapplied candidate does not influence requests; approval/application activates it; restart preserves it; rollback removes it.
- [Per-item desktop receipt](/Users/abc/Documents/overkill/.cache/sequential-context-native/receipt.json): a different workspace is actually inspected first; three selected-workspace item requests each contain only their relevant file markers; final reports remain visible; restart preserves the complete transcript and performs no replay.
- [Full required Go race suite](/Users/abc/Documents/overkill/.cache/skill-context-full-race-final.log): 107 packages passed, nine packages contain no tests. Required PostgreSQL, local-database and frontend gates were enabled. Vet and Windows source cross-compilation also passed.
- [Complete mandatory desktop suite](/Users/abc/Documents/overkill/.cache/skill-context-desktop-full.log): all 93 tests passed, with zero skips/failures, against the final prepared backend.

The backend was independently rebuilt with the same deterministic development build flags; its SHA-256 matches the backend used by native acceptance. Current source and evidence hashes are recorded in [the application receipt](/Users/abc/Documents/overkill/.cache/skill-context-acceptance.json).

Detailed contracts: [evolution storage/provider/review](/Users/abc/Documents/overkill/docs/skill-evolution-acceptance.md), [per-item execution and resume](/Users/abc/Documents/overkill/docs/sequential-context-acceptance.md).

## Limits and remaining work

Relevance uses lexical matching, with explicit paths preferred; it is not semantic code indexing. Source and checkpoint sizes are bounded, multi-item attachments are rejected, and existing tool permissions remain in force. Records are capped at 512 entries/4 MiB/30 days; drafts are capped at 256 and require manual review. Unknown model prices cannot support configured dollar caps, and omitted generation usage cannot publish a proposal. Admission checks do not reserve an invoice amount. Redaction and prompt-injection scanning are heuristic defenses.

No live paid-provider or host Keychain acceptance is claimed. Windows compilation does not establish Windows runtime behavior. Durable externally anchored audit exports, measured savings proof and cloud tenant isolation remain separate open requirements. No compliance, exclusivity or permanent-learning guarantee follows from these tests.
