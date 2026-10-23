# Reviewing uncertain task usage

A stopped coordination task can retain uncertain accounting after a crash, a failed usage write, or a completed provider response without complete usage/prices. The task's budget owner can now review that uncertainty in the desktop task inspector or terminal Usage view. Reconciliation records a manual accounting attestation. It does not reconstruct an invoice, certify a provider report, replay a request, change its state, or declare its prior effects safe.

Open **Reconcile uncertain usage**, choose an affected message, inspect its recorded totals and attempts, and enter full replacement totals for **all recorded attempts of that message**. Historical usage samples have no execution-owner field, so this operation does not pretend to attribute old samples to individual runs. Input/output/cache totals cannot fall below recorded or previously attested values, and cached input cannot exceed total input.

Choose an explicit basis:

- **External record:** cite the record you reviewed. The application records your assertion without independently validating the source. Unpriced estimates may decrease to the already known/attested cost floor.
- **Conservative estimate:** the amount also cannot fall below the current message cost estimate. This remains an estimate, not a provider invoice.

A bounded reason and evidence/source are required, together with explicit acknowledgement. Accepted totals become a floor for later attestations. Other messages' usage and uncertainty remain intact. Token/cost exhaustion remains a blocker. Review budget/replay is a separate deliberate operation after inspecting effects; successful accounting reconciliation never queues work automatically.

## Authority and evidence

The application derives the actor from the selected persisted chat. Only the current task budget owner can mutate accounting. That owner can discover metadata for uncertain delegated child messages in the owned task, even when ordinary message payload reads are denied. Other participants can inspect only their ordinarily visible messages. Cross-project actors and model execution contexts cannot perform reconciliation, and no model-facing reconciliation tool was added. Metadata responses contain no hidden message payloads or provider credentials.

A known authorized message selects the scope. Candidate discovery is bounded and paginated, with explicit continuation/truncation. Each evidence snapshot includes message/usage revisions, observed lower bounds, current contribution, known cost floor, run/sample counts, bounded previews and prior attestations. Previews contain at most 128 runs, 128 samples and 16 prior records; counts and truncation remain explicit. The SHA-256 evidence fingerprint covers **all** ordered rows, including rows beyond those previews.

Mutation pins both revisions and the exact fingerprint. It rechecks current authority, stopped work and recipient ownership inside the transaction. A live recipient lease, any still-started recipient work or changed evidence prevents commitment. Recipient and lease-row locks fence acquisition and in-flight renewal before eligibility is evaluated. Read-only snapshots do not hold the lease row against heartbeats. Expired leases may be removed only inside the fenced mutation. This remains cooperative application ownership; it cannot stop an external context-ignoring action or prove that missing upstream effects never occurred.

The application preserves every original usage sample and execution journal. Immutable reconciliation records hold the before/after totals, actor, basis, reason, evidence and `manual_attestation` provenance; separate links identify exactly which uncertain samples/runs were resolved. A unique UUID and exact request hash make an identical retry return its original immutable receipt. Changing the body under that UUID fails. That receipt describes its original commit; clients refresh current accounting afterward and label a historical snapshot if refresh fails.

One transaction applies deltas to the shared task aggregate, removes only targeted uncertainty counters, stores resolution links and appends the audit event. The message revision advances for this audit event while its state, attempts and deadline remain unchanged. Any failure rolls everything back. A later explicit replay receives a fresh execution-owner identity and a new journal; an older resolution cannot hide its uncertainty. Beginning a finished/resolved journal under a reused owner is rejected.

## Application RPC contract

Fixed authenticated operations are:

- `coordination.reconciliation.candidates`: `session_id`, a known `message_id`, optional `after_id`, and `limit` from 1–50. Clients use pages of 25.
- `coordination.reconciliation`: `session_id` and target `message_id`; returns the exact metadata/evidence snapshot.
- `coordination.reconcile`: `session_id` and typed `input` containing target ID, expected message/usage revisions, expected journal fingerprint, reconciliation UUID, explicit token/cache/cost totals, `usage_known: true`, `price_known: true`, basis, reason, evidence and `acknowledge_uncertainty: true`.

Amounts are nonnegative, finite and bounded: each input/output count is at most 10¹², cache is at most input, and cost is at most one million USD. Reason is at most 2,048 UTF-8 bytes and evidence at most 8,192; both must be nonblank. Database work has a ten-second context. Strict input handling rejects unknown, duplicate, aliased, null, missing and incorrectly typed fields.

A definitive stale conflict returns `committed: false, conflict: true`; it is not an accepted mutation. Clients retain editable text but require an explicit evidence refresh and renewed confirmation. Success returns `committed: true` plus the receipt and accounting snapshot. Authentication/validation/database failures remain errors. An ambiguous transport error retains the exact UUID/body for a deliberate retry; clients never silently submit a replacement UUID or replay work.

## Executed acceptance

Required real PostgreSQL race acceptance passes 134 coordination tests/subtests with zero skips (`.cache/usage-reconciliation-final.jsonl`), plus vet (`.cache/usage-reconciliation-vet.log`). New cases exercise hidden-child metadata with denied payload access, finished sample-only uncertainty, successive attestations and both cost floors, full fingerprints beyond bounded previews, concurrent identical receipts, live ownership and private execution rejection, unchanged original rows, preserved unrelated uncertainty/exhaustion, and an injected final-audit failure proving atomic rollback and exact retry.

A deterministic renewal test holds a valid lease extension uncommitted until the old visible expiry passes. Reconciliation waits on that actual lease row, then observes the committed renewal and returns busy without an audit/delta. The actual same dispatcher is also exercised through lost usage, manual attestation, explicit review/replay and a second unconfirmed run with a distinct owner.

Authenticated API, compiled terminal and native desktop acceptance have separate receipts. The native controlled provider omits usage; the budget owner records an attestation, the task stays blocked and the provider call count remains one after application/database relaunch. No paid provider, invoice verification, external tool-charge accounting, hard spend reservation or whole COORD-06 completion is implied. Distribution and installed-app storage acceptance remain separate from this source contract.
