# Same-hub coordination runtime acceptance

The original COORD-03 criterion asks for six agent-facing ACP/durable operations and a frontend/backend handoff while independent work continues, with the answer delivered at a safe turn boundary. It does not require a foreign-hub/cloud provisioning control plane. The original COORD-06 criterion asks for bounded busy/cancel/deadline/cycle/hop/accounting behavior and blocked evidence for circular requests; it does not require reconstructing provider invoices or reserving every external charge. This document maps those original requirements to the implemented runtime and actual assertions.

## Explicit per-chat ACP transport

Register the chats in the existing project registry and privately provision their scoped credentials as described in [peer ingress](coordination-peer-ingress.md). Then explicitly bind each chat through the **running application's authenticated API**, which owns session admission:

```sh
overkill agent-peers bind-acp --session REGISTERED_CHAT_ID \
  --app-endpoint http://127.0.0.1:APP_PORT \
  --app-token-file /absolute/private/application-api-key \
  --endpoint http://127.0.0.1:ACP_PORT \
  --credential-file /absolute/private/this-peer-key
```

The application token authorizes the application-only configuration RPC; it is never accepted as a peer credential by the ACP ingress. `coordination.transport.bind` takes session ID, explicit endpoint and private credential-file reference. It authenticates the ACP peer and requires exact ID/project/canonical-workspace equality with the local persisted peer. Configuring a backend credential on the frontend chat fails. A foreign hub with different identities/workspaces fails. Endpoint dialing uses the existing protected peer client. No model schema contains endpoint, token, credential path, sender, project or execution capability fields.

The binding persists per session, including its exact peer snapshot and revision; only a private local file reference is stored, not secret bytes. Runtime assembly reads that private file, authenticates again and installs the six ACP-backed tools. Their send/reply/status/etc requests reach the actual HTTP client/server/store. Graph/contract services, filesystem guards, accounting and dispatch remain local to the same hub. Advertised remote workspace metadata never grants local file access.

Binding/clearing owns session admission through verification, persistence and disposal of already-loaded runtime resources. An active turn refuses reconfiguration. Before cached agent reuse, a runtime callback compares persisted binding/identity and private-file contents; a change requires rebuilding before the next admitted turn. Non-admitted extension inspection must obtain admission before any cache check/discard. Busy bridge resolution returns its bound control adapter without touching the active runtime cache, preserving busy/stop controls. Idle resolution can assemble under admission for pre-turn gateway callbacks. Neither path can discard a running agent when a private file changes. Already-admitted send/stream/audit/worker paths use a distinct internal primitive to avoid recursively entering admission. Credential/transport failure fails closed rather than substituting a global token or another peer.

To restore default direct-store tools without requiring the ACP endpoint to be online:

```sh
overkill agent-peers clear-acp --session REGISTERED_CHAT_ID \
  --app-endpoint http://127.0.0.1:APP_PORT \
  --app-token-file /absolute/private/application-api-key
```

These RPCs are trusted application operations, not model tools. They do not allocate a foreign executor, transfer project ownership, or create sessions implicitly. Operator changes must target the application which owns that interactive chat; this is not a multi-host interactive-session configuration protocol.

## Hidden execution ancestry

A dispatched execution receives a fresh random 256-bit transport capability in its private context. Only its hash is stored beside the live lease. The six-tool adapter injects a private execution envelope from that trusted context when sending/replying; a model cannot create it through tool arguments. The ACP server derives actor identity from the scoped credential, then revalidates capability hash, live owner, parent state and project inside the existing mutation transaction. Knowing message/owner IDs alone is insufficient.

A child inherits the stored parent conversation/task/deadline/depth and required contract pins. Model attempts to reset those fields cannot detach the child from accounting, cycles or dependencies. A stale/revoked owner cannot create new effects; retrying a preexisting idempotent receipt remains effect-free. Raw manual ingress retains its separate ownership fences and explicit no-replay recovery described in peer ingress. This is transport for the same executing hub identity, not a foreign worker lease protocol.

## Clause-to-assertion evidence

| Original clause | Production consumer | Executed assertion |
| --- | --- | --- |
| COORD-03 six ACP-backed tools | `runtimeServices.sessionCoordinationTools` → `NewBoundRemoteCoordinationTools` → `acp.Client.Coordination` → scoped ACP ingress/store | `TestRemoteCoordinationSixAgentToolsUseActualScopedACP` exercises all six; `TestSameHubACPBoundModelRuntimeHandoffAtSafeBoundary` inspects the actual model catalogue and counts real HTTP send/reply calls. |
| Independent frontend work and correlated backend answer | Registered runtime factories, native filesystem tool, same-hub store and dispatcher | The same runtime test creates a real frontend artifact while the backend is running, executes backend model `agent_reply`, checks task/conversation/reply links, and requires the pending frontend answer to remain accepted while its interactive turn is held. |
| Safe turn delivery and durable provisioning | API session admission through history save, dispatcher `Reserve`, persisted per-session transport | The runtime test releases the first turn, observes a separate answer turn and persisted transcript, rejects active-turn rebind, discards the prior loaded catalogue, reloads binding in fresh assembly and clears it back to local transport. |
| No active-runtime teardown by readers | Non-admitted `getOrCreateAgent` admission wrapper vs admitted execution primitive | `TestRuntimeTransportChangeCannotDiscardActiveTurnFromReaders` changes a real private file during a blocked provider call, invokes extension inspection and bridge resolution, proves no cleanup/cancellation, then rebuilds after the turn commits. |
| COORD-06 busy queue / safe updates | Durable inbox, dispatcher reservation and shared session admission | `TestDispatcherLeavesBusySessionUnclaimed`, `TestCoordinationAutomaticDeliveryUsesBoundSessionAndWakesSender`, plus the actual bound-runtime handoff above. |
| Cancellation | Durable participant transition plus dispatcher heartbeat/context | `TestDispatcherCancellationStopsOwnedExecution`; `TestCoordinationIngressFencesManualOwnershipAllowsLiveCancellationAndRecovery` proves remote cancellation during live owned work and refusal of late completion. |
| Bounded queue/concurrency | Project admission and dispatcher active slots | `TestConcurrentPeerQueueAdmissionAndDurableEscalationScope`, `TestProjectQueueBoundAcrossRecipients`, `TestTerminalAnswerReserveCannotBeReused`, `TestDispatcherEnforcesConfiguredGlobalConcurrency` (five targets, exactly two held active slots). |
| Deadlines, hops, attempts | Stored deadline/private lineage and bounded execution attempts | `TestDispatcherExpiresQueuedMessageWithoutExecuting`, `TestDependenciesDeadlineAndLeaseFencing`, `TestRequestDepthBoundSurvivesStoreRestart`, `TestConversationRequestBudgetAndIdempotency`, `TestExecutionAttemptBudgetRequiresDurableReview`. |
| Cycle blocking and evidence | Transactional ancestry validation and durable participant-scoped escalation | `TestDispatcherRejectsCircularRequestsWithDurableEscalation`, `TestActualAgentCircularRequestStopsWithReadableEscalation`, and `TestSameHubTransportExecutionProofPreservesAncestryAndRejectsForgery`. Circular child requests are rejected and receive blocked, requires-review evidence; the accepted parent may finish with an explanation. No circular child is executed. The actual-agent fixture limits provider work to at most three calls. |
| Task-level usage accounting | API provider observer → lease-fenced journal/samples/shared task totals | `TestTaskUsageSharedAcrossPeerExecutionsAndConversations`, `TestTaskUsageCostUnknownAndConcurrentAccounting`, `TestTaskUsageJournalExposesLostExecutionBeforeFurtherSpending`, `TestActualPeerTaskUsageStopsProviderAndSideEffects`, `TestPeerCostBudgetRejectsAmbiguousOrIncompletePrices`, `TestFailedUsageWriteNeverCertifiesExecutionJournal`. |
| Explicit escalation for unresolved dependencies | Durable escalation pages, dependency failure propagation and owner review/reconciliation | `TestDependenciesFailureCascadeIsDurableAndNeverExecutes`, `TestInboxExposesDurableBudgetEscalationPages`, `TestReviewRequestAtomicBudgetReceiptRestartAndWorkerClaim`, and the usage reconciliation authorization/evidence/rollback/replay fixtures. Inspection or accounting attestation never automatically replays work or resolves unrelated uncertainty. |

The transport proof fixture uses a real dispatcher/private context and actual ACP requests. It denies forged known-ID proofs, preserves all inherited fields/contract pins, rejects a circular child with blocked evidence, rejects stale capabilities, and checks that receipts contain no raw capability. It does not infer ancestry protection from a standalone client mock.

## Limits

Queue limits are 128 messages per recipient, 512 per project, 32 requests per conversation, eight hops and three execution attempts. Default dispatcher concurrency is four (trusted configuration permits 1–64). These are protocol bounds. Shared task limits are checked between model calls, with explicit unknown/unconfirmed usage and manual reconciliation; in-flight/concurrent requests can exceed a remaining allowance. No hard invoice ceiling, external tool-charge accounting, provider billing reconstruction or paid-provider quality is claimed.

Acceptance uses a controlled local provider and real isolated PostgreSQL with mandatory database configuration and race detection. Same-hub ACP transport keeps the original local dispatcher/accounting/admission authority. Foreign-hub/deployed-cloud executor leases, arbitrary separate database replicas and cross-host interactive ownership are unsupported by this binding. Desktop/TUI source, installed applications and the frozen development DMG were not modified by this lane.
