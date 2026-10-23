# Contract requirements on agent requests

A coordination request may include `required_contracts`, an immutable array of
`{"contract_id":"<canonical UUID>","revision":1}` pins. At most 32 distinct
contracts are permitted. Each contract must exist in the authenticated sender's
project at exactly that revision when the request is first accepted. The sender
need not own the contract: agreements are project shared. Other message kinds
cannot supply requirements in submitted input. Application-derived causal
notifications and answers inherit requirements internally because their
recipients can also execute tools.

Pins are durable request fields and part of the idempotency envelope. An empty
or omitted array retains the legacy envelope hash. Retrying the same accepted
envelope retrieves its original receipt even after an agreement changes or is
deleted; changing its pins requires a new idempotency key and deliberate request.

## Causal inheritance

Every new message sent by a live dispatcher execution inherits the union of its
parent's requirements and its submitted request requirements. A child cannot
remove inherited pins, replace their revisions, or exceed the combined 32-pin
bound. Descendants carry the full union, including across multiple recipients
and levels. Correlated replies also inherit the original request's pins;
app-only inheritance permits notifications to carry pins even though explicit
nonrequest input is rejected.

Before accepting causal work, the store rechecks the parent's current pins and
its executing recipient's recursive acknowledgements. A stale parent is failed
and a missing agreement blocks it before a new child can be accepted. Each new
recipient must acknowledge its inherited pins independently; the parent's
acknowledgements do not authorize the child's effects. This includes the sender
receiving an automatic answer notification: the answer is persisted and visible,
but its agent cannot execute until that sender acknowledges the agreements.

The idempotency digest covers the original submitted envelope and private parent
identity before inheritance. An identical accepted retry returns the stored
receipt even if its parent subsequently completes or is invalidated; this does
not accept new work, advance pins, or bypass a child's execution gates. A new
idempotency key must pass current parent/lease/contract validation.

## Dispatch and agreement readiness

A recipient may claim work only after all request prerequisites complete and
all required agreements are ready. The recipient must acknowledge every pinned
root revision. Recursively, each dependent contract's owner must acknowledge its
upstream contracts' current, pinned revisions. Disabled contract owners and
missing acknowledgements prevent readiness. These are protocol agreements, not
proof that tests passed or artifacts satisfy acceptance criteria.

Requests waiting for acknowledgements remain `accepted`, with no execution
attempt consumed. Independent ready work can dispatch past a waiting request.
A deleted root contract, changed root revision, or changed transitive dependency
revision fails unstarted work durably with a `required_contract_failed` event
payload and review escalation. Request dependency failures then propagate by the
ordinary durable dependency rules. A fresh request must explicitly pin the new
agreement; stale pins are never silently advanced.

The payload-free graph accepts any participant-visible message as its root,
including inherited notifications; request prerequisite targets remain request
only. It includes `contract_blockers` on each node.
Reasons include `required_contract_deleted`,
`required_contract_revision_changed`, `dependency_revision_changed`,
`owner_disabled`, and `current_revision_not_acknowledged_by:<peer ID>`.
Waiting agreements produce readiness `waiting`; invalidated revisions produce
`dependency_failed`. Started and terminal request states remain visible as
`active` and `terminal`, with current blockers still included.

## Effects and completion

The application attaches private execution lineage to actual dispatcher runs.
Both owned-path and unrestricted-workspace effect guards recheck that run's
lease, current request state, pinned revisions, and recursive acknowledgements
before their callback. They hold the shared project contract advisory lock
through the synchronous effect, so contract updates cannot race between this
check and a guarded write. A changed agreement returns `ErrRequestContracts`
without executing the callback. This is an application guard, not an OS sandbox.

Worker completion, correlated answer/completion messages, explicit completion
transitions, and recovery resume also recheck requirements. A stale agreement
fails the request; a temporarily missing acknowledgement blocks it for deliberate
recovery. Both persist a review escalation and suppress a successful answer.
The dispatcher reconciles requirements after every run outcome, including tool
errors and cancellation, and preserves the gate's durable state instead of
reclassifying blocked agreement work as a generic failure. Reconciliation does
not duplicate an existing blocked/terminal decision or escalation.
Consuming a failed tool's error as model input cannot bypass the final agreement
gate. Previously completed receipts remain completed; later agreement changes
do not retroactively undo completed work.

Admission operations acquire their existing project admission/peer/message locks
before the contract lock. Contract mutation and guarded effects never acquire
admission. No contract lock spans provider inference. Callbacks must follow the
existing guard rule against recursively calling contract APIs on another
connection.

Requirements neither create nor manage Git worktrees. Task IDs and worktree
metadata do not by themselves establish request prerequisites or agreements.
This feature does not establish complete scheduler/worktree/contract parity with
other coding harnesses.

## Verification

`internal/coordination/request_contracts_test.go` exercises actual PostgreSQL
persistence and restart, acknowledgement gating and independent dispatch,
stale/deleted revisions and cascading request failure, idempotency compatibility,
project scoping, two-level causal inheritance and recipient acknowledgements,
notification effect fencing, conflicting child versions, stale-parent rejection
and original receipt retrieval, transitive acknowledgements/revision invalidation, effect refusal,
concurrent update serialization, and suppression of completion through worker,
reply, and explicit transition paths. Actual dispatcher workers also verify
missing acknowledgements, disabled owners, and stale revisions after successful,
guard-error, and cancelled run outcomes, including exactly one durable decision
and review escalation. These use local controlled requests and do
not claim live provider or cross-host deployment acceptance.
