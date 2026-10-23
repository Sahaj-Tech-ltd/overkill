# Coordination usage reconciliation

Implemented in the desktop workspace and compiled Ink Agents inspector. This is a deliberate operator accounting action, separate from **Review budget and replay**. Reconciliation never requeues a message or makes a provider call.

## Review contract

The operator opens **Inspect task → Inspect usage evidence** (Ink: usage inspector, `u`). A paginated metadata-only picker discovers uncertain messages in the selected task, including hidden child messages for the task's budget owner. Ordinary participants retain their existing visibility limits. Hidden child payloads are not returned or fetched by the correction flow.

The selected snapshot covers **every recorded attempt for one message**. Historic usage samples have no run-owner attribution, so the UI does not offer fictitious per-attempt corrections. It displays observed lower bounds, current contribution, eligibility, the known cost floor, and previous immutable manual attestations. Detailed revisions, fingerprint, journal and sample metadata are collapsed in the desktop dialog. The form and receipt remain scrollable. The left-aligned acknowledgement explicitly warns that later reviews cannot lower the attested totals and that this does not replay the request.

The operator enters all token/cache totals and USD cost explicitly, chooses a basis, cites evidence and a reason, and confirms that they reviewed prior effects. Missing usage is never prefilled as zero. Both bases are **manual attestations**, not independently verified invoices or provider reports:

- `conservative_estimate` preserves the current contribution's estimated cost.
- `external_record` means the operator cites an external record; it may reduce unpriced estimates only as far as the backend known cost floor.

Both preserve observed and previously attested token/cache lower bounds. The backend enforces budget-owner authority, stopped execution, absent live recipient lease/started work, and exact accounting/message evidence under its admission and database locks. Existing samples/journals remain intact. Other uncertain messages and budget exhaustion may remain after correction.

## Transport and retry

Fixed authenticated RPC methods are `coordination.reconciliation`, `coordination.reconciliation.candidates`, and `coordination.reconcile`. The API derives the peer from a persisted `session_id`; the body cannot choose owner identity. Strict bounded JSON decoding rejects duplicate, unknown, null or misspelled fields. Desktop uses three named typed IPC operations, with no generic renderer RPC.

A write captures the exact message revision, usage revision, journal fingerprint and immutable reconciliation UUID. The body includes explicit totals, certainty flags, evidence basis, reason/evidence and acknowledgement. A definitive authorized store conflict returns `{committed:false,conflict:true}`. The UI retains text but requires refreshed evidence, a new confirmation and a new UUID before resubmission. Authorization, validation and ambiguous database failures remain errors.

An ambiguous transport outcome freezes the exact body and UUID for a **manual** retry; no silent retry or revision refresh occurs. An accepted response explicitly returns `committed:true`. Exact UUID replay can return a historical immutable receipt, so success refreshes current snapshot, candidates and usage/message metadata before presenting current totals. If refresh fails, the accepted receipt remains labeled historical. The audit increments message revision but does not change message state, attempt count or replay behavior.

## Acceptance receipts (2026-10-22)

- `.cache/usage-reconciliation-api-race.log`: authenticated real-PostgreSQL API race test passed. Public Dispatcher execution lineage creates a real hidden child; budget-owner metadata discovery/pagination works while payload read remains forbidden. Nonowner writes fail, stale CAS is explicitly uncommitted, accepted correction preserves state, exact receipt replay is idempotent, and strict malformed JSON fails.
- `.cache/usage-reconciliation-desktop-unit.log`: 21 desktop unit checks passed, including explicit fields/no replay, hidden-child metadata, conflict draft retention/new evidence/new UUID, frozen ambiguous retry, historical receipt, read-only eligibility and basis cost floors.
- `.cache/usage-reconciliation-tui-typecheck.log` and `.cache/usage-reconciliation-tui-build.log`: compiled successfully.
- `.cache/usage-reconciliation-tui-tests.log`: 25 compiled coordination checks passed, including actual authenticated HTTP fixed reconciliation methods, model conflict/new-CAS flow, exact ambiguous retry, no replay operation and actual Ink manual-accounting display.
- `.cache/usage-reconciliation-native.log`: mandatory Electron + real backend + managed PostgreSQL acceptance passed, one test, zero skips. A controlled actual provider omitted usage; the owner corrected uncertainty through the real desktop dialog. The request stayed blocked, provider calls stayed at one, and app/database relaunch retained immutable `manual_attestation` provenance and evidence.
- `.cache/usage-reconciliation-ui-final.log`: final compiled Ink and desktop UI checks passed, 46 checks, zero skips, after acknowledgement/checkbox polish.
- `.cache/usage-reconciliation-native-receipt.png` and `.cache/usage-reconciliation-native-evidence.png`: actual desktop receipt and evidence views with totals, basis, history and explicit no-requeue outcome. The dialog uses collapsed technical evidence.

The native fixture uses disposable fake fixture credentials and simulated safeStorage, excluded from packaged sources. It proves accounting/product behavior, not production Keychain access or installed-DMG acceptance. The prior installed Keychain requirement remains separate and pending. No DMG was rebuilt for this lane.

## Implementation locations

API: `internal/api/usage_reconciliation_handlers.go`, its real-PG HTTP test, and fixed dispatch in `server.go`. Desktop: `desktop/src/usage-reconciliation.js`, `usage-reconciliation-input.cjs`, named main/preload IPC, workspace task control and dialog styles/script allowlist. Ink: `tui/src/backend/usage-reconciliation.ts`, coordination model/panel, and compiled `tui/tests/coordination.test.mjs`. Backend accounting implementation and transactional safety are documented by the corresponding coordination store tests.
