# Bounded peer work

Coordination admission is serialized within each project. Requests are limited to eight hops, 32 requests per conversation, 128 pending/started/blocked messages per recipient and 512 per project. Persisted execution attempts stop at three. Replaying the identical idempotency key preserves its original receipt. The first valid correlated terminal answer can bypass queue pressure; repeated fresh-key answers cannot reuse that reserve.

A dispatcher attaches private execution ancestry to its context. Child sends revalidate the live lease and parent, inherit its conversation/task/deadline, and cannot reset ancestry through model arguments. An A→B→A request chain, including a fresh request triggered by a terminal answer, is rejected. Protocol limits survive store recreation. These protocol bounds are separate from the shared task token/cost accounting below; neither promises a hard upstream invoice ceiling.

Admission rejection records a separate durable escalation while returning a typed error. The rejected child message was not accepted. Escalations have `state: "blocked"`, `requires_review: true`, reason, participants and parent/conversation references. The parent may still complete with an explanation; its completion does not resolve the escalation.

`agent_inbox` includes `escalations`, `next_escalation_cursor` and `has_more_escalations`. Pass `after_escalation` and `limit` (1–200, default 50) to page forward. Authenticated application clients can call `coordination.escalations` with `session_id`, `after` and `limit`, receiving `escalations`, `next_cursor` and `has_more`. Session binding determines identity; sender/project overrides are rejected. Only enabled participating peers in the same project can see an escalation, including its metadata. Unrelated project peers do not gain access. Cursors may contain gaps because visibility is filtered.

Store tests exercise circular chains, bounded queues under concurrent admission, deadlines, restart-persistent depth/attempts, terminal answer reserve and durable evidence. Tool fixtures verify cursor paging and unrelated-peer isolation. Controlled API provider fixtures exercise actual Agent.Run → agent_send → store context propagation; no paid provider is contacted. Inspection does not automatically resolve an escalation or repair dependencies.

## Shared task usage

Authenticated application clients can attach an optional `usage_budget` when creating a root request through `coordination.send`:

```json
{
  "session_id": "frontend-session-id",
  "to": "backend",
  "kind": "request",
  "task_id": "users-endpoint",
  "payload": {"question": "Implement GET /users"},
  "idempotency_key": "users-endpoint-v1",
  "usage_budget": {"tokens": 100000, "cost_usd": 2.0}
}
```

Zero disables that component's limit. Limits are committed atomically with the root request; retrying the identical request/budget returns its original receipt. Model-facing `agent_send` cannot create or replace limits. A task's initial sender owns its root identity: unrelated peers cannot start another root against that task ID. Executing children inherit task/conversation identity from the stored parent. The same task ID under that original sender aggregates usage across conversations and peers; without a task ID, accounting uses the conversation. The budget owner may explicitly change limits and review/requeue eligible blocked work using the revisioned, idempotent [request review](coordination-request-review.md) control. Review preserves all previously recorded usage; it does not reset counters or erase uncertainty.

`coordination.usage` takes `session_id` and `message_id`, and returns participant-filtered `usage`: provider-reported input/output/cache tokens, configured-price cost estimate, limits, revision, unknown usage/price counts, pending/unconfirmed executions and review reason. Cached input is part of total input, and receives replacement cache pricing rather than being charged twice. Provider/model metadata accompanies ordinary, auxiliary and streaming accounting; goal accounting remains independent and is not subtracted from task totals. A stream contributes its final cumulative snapshot once, including late cache fields. Interrupted/failed streams retain their observed tokens but do not claim complete usage. Missing usage, ambiguous provider prices or an unconfigured zero rate for a consumed category require review when the corresponding limit is active. Legacy adapters that do not mark response usage complete can conservatively trigger this condition even if some numbers are available.

Each execution is journaled before model work. Samples are idempotent, lease-fenced and transactional. Lost/revoked workers and failed usage writes leave an unfinished journal; after lease release/expiry, its usage becomes explicitly unconfirmed. A later peer cannot spend under a configured task budget while that execution is unresolved. A normal message cancellation does not discard already-returned usage; persistence uses a bounded detached context. Crash recovery cannot reconstruct an upstream invoice or missing tokens. The budget owner can explicitly [reconcile uncertain usage](coordination-usage-reconciliation.md) with a bounded manual attestation after stopped ownership is fenced; original samples/journals and audit history remain preserved. Reconciliation does not replay work.

Limits are checked between model calls, not reserved against a provider invoice. A request already in flight—and concurrent requests admitted before another peer reports usage—can exceed the remaining allowance. Cost is an estimate from explicitly configured model prices; it is not an account balance, subscription quota or a guarantee of a hard dollar ceiling. Auxiliary calls still using an unavailable/ambiguous price are recorded as unknown. External tool/API charges are not included. These are the task-level accounting boundaries. The original COORD-06 criterion does not promise provider invoice reservation, external tool-charge accounting or every foreign transport integration; those are not inferred from the executed local/same-hub acceptance.

Executed acceptance includes real Agent.Run/provider/tool paths which stop before a tool side effect when token/cost usage is exhausted or missing, durable cross-peer task totals, concurrent sample admission, restart/idempotency, partial stream and startup/cancellation failures, and a PostgreSQL-triggered usage-write failure which must preserve an unconfirmed execution after database recovery. No paid provider was contacted.
