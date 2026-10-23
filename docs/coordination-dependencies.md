# Durable request dependencies

Coordination request messages are execution handles. A new request can set `depends_on` to at most 32 distinct, canonical request UUIDs. Dependencies must already exist in the same project, and the authenticated sender must be a participant in each prerequisite. Notifications, answers, unknown IDs, duplicates, and future IDs are rejected. Dependencies cannot be edited after acceptance. These existing-only, immutable references form a directed acyclic graph without a mutable graph editor or speculative future nodes.

The submitted dependency order is part of the idempotency envelope. Reusing an idempotency key with different dependencies is rejected. Omitting dependencies, or submitting an empty list, preserves the previous envelope hash and existing receipts. Acceptance and the dependency list commit in the same database transaction. A fresh runtime reconstructs the graph from PostgreSQL rather than process memory.

## Execution and failure

Claim selects the oldest eligible message whose prerequisites are all `completed`. A waiting request remains `accepted`; it does not claim the peer or block unrelated ready work. Dependency readiness does not override existing queue admission, deadline, peer enablement, lease fencing, execution-attempt limits, budget checks, or explicit interrupted-work recovery.

Before claiming ready work, a recipient's claim transaction fails accepted requests whose prerequisite is `failed` or `cancelled`. It records a `failed` event with `reason: dependency_failed` and explicit prerequisite blockers, plus a durable escalation requiring review. No execution attempt is consumed. Subsequent dispatcher polling propagates this failure through dependent recipients, including after restart. This is eventual propagation through each recipient's claim path, rather than an immediate project-wide cascade. An expired unstarted request receives the existing `deadline_elapsed_before_start` failure before dependency processing. A peer held by started or blocked work retains its existing admission behavior; its waiting requests are processed when claiming can resume.

Failed or cancelled prerequisites never become successful through automatic retry. A caller must create a new request (and new dependent requests if needed); immutable existing dependencies continue to refer to the original result. A completed state is a reported task outcome, not proof that tests passed or an interface contract was acknowledged.

## Graph inspection and privacy

`Store.Graph(ctx, viewerPeerID, messageID, limit)` returns a request and its prerequisite ancestry in one repeatable-read snapshot. A zero limit defaults to 100 visible nodes; supported explicit limits are 1–200. `truncated` reports a visible traversal cut short by this bound. Traversal is breadth first, deduplicates shared prerequisites, and preserves each message's submitted dependency order for blocker metadata. It returns ancestors, not project-wide or reverse dependent discovery.

Nodes contain participant-visible message metadata with `payload: null`; full payloads remain available through the ordinary scoped message read. Node readiness is `ready`, `waiting`, or `dependency_failed` for accepted work; terminal messages show `terminal` and started/blocked messages show `active`. Blockers include prerequisite ID, state, and reason. Readiness describes dependencies only; it does not promise that a worker is currently available or that a provider will succeed.

A recipient may receive a request whose prerequisite belonged to its sender and another peer. For such an explicitly referenced prerequisite, inspection intentionally exposes only its ID and state as scheduling metadata. It withholds its envelope, peer identities, task/conversation IDs, payload, and ancestry. `hidden_dependencies` indicates this withheld content. Knowing a dependency ID does not authorize using it as a graph root or adding it to another request. Roots outside the viewer's own correspondence, including other projects, return not found. Disabled viewers cannot inspect graphs.

This graph schedules existing coordination request execution. It does not turn the independent file-based commit tracker into a scheduler, create worktrees, assign ownership paths, or require contract readiness. Those integrations remain separate delivery work.

## Verification

Real PostgreSQL tests in `internal/coordination/dependencies_test.go` cover waiting versus independent dispatch, concurrent recipient claims, database-backed runtime reconstruction, prerequisite completion, failed/cancelled cascades and durable escalations, no duplicate retry on polling, participant/project isolation, graph truncation and hidden metadata, invalid input, immutable idempotent receipts, legacy hashes, deadlines, and lease fencing. Existing coordination tests remain necessary for the surrounding dispatcher, budgets, callbacks, and recovery protocols.
