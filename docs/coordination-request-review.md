# Reviewing a blocked agent request

The trusted application can review a blocked request, change its shared token/cost limits and put the same request back in the accepted queue. A review is an explicit replay decision: earlier tool effects can repeat. It does not resume a suspended model call or prove that earlier effects rolled back.

`Store.ReviewRequest(ctx, authenticatedPeerID, ReviewRequestInput)` accepts:

| Field | Meaning |
| --- | --- |
| `message_id` | Canonical UUID of the blocked request |
| `expected_revision` | The message revision shown to the reviewer |
| `expected_usage_revision` | The shared usage revision shown to the reviewer |
| `review_id` | Fresh canonical lowercase UUID; retain it for an ambiguous retry of the exact same body |
| `note` | Nonblank valid UTF-8 review explanation, at most 2,048 bytes |
| `limits` | Explicit `tokens` and `cost_usd`; zero denotes unlimited for that dimension |
| `requeue` | Must be `true`, acknowledging a new worker execution attempt |

The result contains `message` and `usage`. `TaskUsage.owner_peer_id` comes from persisted task ownership; it is display metadata, never a caller-supplied authorization claim. Only an enabled peer that is both a correspondent of the request and the persisted owner of its usage scope can review. A child request's sender cannot take over the root owner's budget, and root ownership alone does not grant access to a child's private payload. Repository-scoped peers must still pass live checkout identity validation. Disabled peers cannot replay an old review receipt.

The transaction checks both revisions. It refuses terminal, accepted, started or non-request messages; an elapsed original deadline; exhausted execution attempts; a live recipient execution lease; another started request for that recipient or shared task; unfinished execution journal entries; and limits that would already be exhausted. Unknown usage or price cannot be bypassed by turning off a previously enabled relevant limit. Unknown usage blocks when either old or proposed token/cost limits are enabled; unknown pricing blocks when either old or proposed cost limits are enabled. Unfinished journal entries always block, including unlimited tasks. The operation never repairs, deletes or marks uncertain accounting records as confirmed.

On success, limits and usage revision change together with `blocked → accepted` and the message revision. All cumulative token/cost counters, samples, execution journals, original deadline, execution attempt count, dependency IDs, agreement revision pins, conversation, task and causal lineage remain intact. A durable `reviewed` event records the actor, review ID, explanation, new limits, new usage revision and replay acknowledgement. Requeue does not resolve earlier escalation history; that history remains evidence of what happened.

A normal worker must subsequently obtain its lease/reservation and claim the accepted request. Its existing dependency, agreement, deadline and attempt gates still apply. For example, review can accept a request whose pinned agreement has since changed; the subsequent worker claim will apply the normal durable stale-agreement failure without executing it. Review never substitutes a new agreement revision or bypasses acknowledgement. Application session admission is still checked by the worker before execution.

An actor/review-ID receipt is committed in the same transaction. Retrying the exact body returns the original committed result even if a worker has since claimed or finished the request. A changed body with the same review ID is an idempotency error. The receipt is historical acceptance evidence; its accepted state is not the latest message snapshot. The caller should refresh current status after showing successful review. Live peer authority, participant visibility and persisted budget ownership are revalidated before receipt replay.

Project admission is locked before recipient, actor, message and usage records, matching the existing send/claim/accounting order. Legacy status transitions, including an explicit blocked-to-started resume by a different peer, also take project admission before peer/message locks. The recipient's exclusive peer lock serializes review with worker lease acquisition. Provider accounting and journal updates acquire the same project admission lock, so review cannot erase or miss an in-transaction usage update. The dispatcher acquires its coordination lease before application runtime reservation; a live coordination lease therefore also fences that reservation path. This is cooperative application control, not an operating-system sandbox or a guarantee against manually executed external effects.

The PostgreSQL tests in `internal/coordination/request_review_test.go` exercise committed receipts across store restart and subsequent claim, concurrent identical/different review IDs, owner and participant boundaries, message/usage revision conflicts, confirmed totals preservation, unknown accounting and unfinished journals, lease/deadline/attempt refusals, real concurrent worker lease acquisition, a cross-peer legacy resume racing review, and agreement/dependency preservation through the next claim. Existing budget, lease, dependency, agreement and repository tests remain required alongside these focused tests.

The authenticated application RPC is `coordination.review` with
`{session_id, input: ReviewRequestInput}`. Its strict decoder rejects unknown
fields and requires an explicit `limits` object containing both non-null
`tokens` and `cost_usd`; omitted fields cannot accidentally turn off a limit.
Sender, project and budget ownership always come from the session/store.

In desktop Tasks, inspect a blocked request as its budget owner, then choose
**Review budget and replay**. Enter the new task-wide limits and a review note,
check the acknowledgement of repeated effects, and submit. After an ambiguous
failure the draft freezes; **Retry same review** submits exactly the same body
and review ID. Close and refresh before attempting a changed review. Old-chat
responses cannot close or relabel a later draft. The recorded escalation list
labels past review triggers as historical evidence and directs users to the
request's current state. Terminal controls and keyboard semantics are documented
in [terminal request review](tui-request-review.md).

Native acceptance blocks a real request on a tiny token limit, reviews and
raises that same task budget, requeues the same request, then observes completion
and the durable reviewed event. It checks that the old usage remains and replay
adds usage; reply delivery to another agent can add further task-wide usage.
The controlled local provider is not an invoice or paid-provider acceptance.
