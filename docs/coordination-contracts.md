# Versioned agent handoffs

Registered project peers receive `agent_contract` alongside the existing durable message tools. The application binds the tool to a persisted peer/session identity. Neither a model argument nor a message payload chooses the owner or project. Authenticated application clients can use `coordination.contract` with `{ "session_id": "…", "input": { "action": "…" } }`; the same typed tool handles that input.

Actions are `create`, `get`, `list`, `update`, `acknowledge`, `readiness`, and `delete`. Updates, acknowledgements and deletion require `contract_id` and `expected_revision`; stale revisions fail. Create/update takes a complete `spec`, containing:

- A name, endpoint, schema version and JSON schema object.
- Acceptance criteria and optional named artifact paths, media types and digests.
- The registered workspace, optional worktree/task references, and owned relative paths.
- Dependency contract IDs pinned to exact revisions.
- Explicit compatibility changes (`additive`, `breaking`, or `compatible`) with old/new versions and a description.

`agent_inbox` exposes the registered workspace. Equivalent absolute workspace aliases resolve to the same canonical identity. Ownership paths reject traversal, symbolic-link components and portable case/Unicode aliases. Claims conflict across the project, including ancestor/descendant claims and claims by disabled peers. A disabled owner does not silently release interrupted work.

Every update creates an immutable revision and invalidates acknowledgements of the earlier agreement. Changing endpoint/schema content additionally requires a new schema version and compatibility record. Dependencies form a checked acyclic graph. `readiness` reports current acknowledgements and pinned recursive dependencies, with explicit blockers for revision changes, missing acknowledgement or disabled owners. **Readiness is agreement on an interface, not proof of task completion or passing acceptance criteria.** Artifact digests and task/worktree references are declarations; they are not independently certified by storing the contract.

The model workflow is to publish a backend agreement, pin it in the frontend agreement, acknowledge the current versions, and exchange a durable `contract_changed` message when revising the interface. Contract updates do not themselves send a notification; use `agent_send` with an idempotency key. The recipient re-reads the agreement, updates its dependency pin and client code, acknowledges the new versions, and runs the combined integration check.

## Coordinated write admission

The shared runtime installs an execution guard on each registered peer's tool registry. Both ordinary and concurrent lookups retain this guard, including dynamically registered adapters; optional tool schemas/descriptions remain intact.

Native `fs` write/edit/mkdir and `patch` hold the project's database lock from ownership checking through synchronous execution. Once any project paths are claimed, a peer must claim its own write paths; it cannot write another peer's paths or unclaimed paths. Native reads/search and read-only LSP queries remain available. Contract/message tools use their own store authentication and transactions.

Opaque tools such as shell and remote adapters cannot safely enumerate their write paths. They are admitted only when foreign reservations are in physically disjoint registered workspaces. Shared or nested roots reject these operations. Contract mutation and synchronous guarded effects share a project lock, preventing claim reassignment between the check and the effect. Different Store/process instances use the same PostgreSQL lock. Lock waiting respects context cancellation. Database rollback cannot undo a file effect that already occurred.

This is cooperative admission for registered peer tools. It is **not an OS sandbox**: external editors, unregistered sessions, user-installed hooks, commands escaping their working directory, detached/background work and deferred remote effects are outside this guard's boundary. Arbitrary asynchronous child execution and task-graph/worktree lifecycle integration still require additional work. No whole COORD-05 completion or host-wide exclusion is claimed.

## Evidence and bounds

The production shared-runtime fixture creates backend/frontend agreements, performs a real backend file change, sends a persisted schema-change message, observes stale dependency readiness, updates/acknowledges the new versions, writes a real JavaScript client and executes it against a controlled HTTP endpoint. A second fixture rejects conflicting actual writes/edits/patches/mkdir/shell effects in a shared workspace and preserves the backend file. HTTP RPC tests verify session-bound ownership, project isolation, rejected forged identity and optimistic acknowledgement.

Store tests cover restart durability, concurrent updates/claims, dependency cycles, immutable revisions, case/Unicode and symlink aliases, callback locking, cancellation and disabled owners. Registry tests verify ordinary/concurrent/dynamic tool admission without erasing optional metadata. These are controlled local tool/protocol fixtures, not autonomous paid-provider acceptance.

## Composed local-provider acceptance

`internal/api/coordination_composed_acceptance_test.go` adds `TestCoordinationComposedFrontendBackendAcceptance`. It uses a controlled OpenAI-compatible HTTP provider, authenticated `agent.send` HTTP ingress, production session admission and coordination workers, native guarded tools, and isolated real PostgreSQL through `internal/testpg`.

The frontend creates its path agreement, requests the backend endpoint/schema through `agent_send`, and writes an independent UI artifact while the request is outstanding. The backend creates and writes the v1 endpoint, sends progress, waits for the frontend to pin/acknowledge v1, then writes a breaking v2 response and updates its contract with a compatibility record. It sends a durable `contract_changed` message. Before finishing its interactive turn, the frontend observes stale dependency readiness; both incoming messages remain accepted and no second frontend turn enters. After that turn saves and releases admission, progress and schema-change messages execute in separate safe turns. The frontend re-reads v2, updates its dependency pin, acknowledges the current agreements, writes a Python HTTP client, executes it through the native shell tool, and reads the generated integration result through `fs`.

The endpoint fixture is a test-owned HTTP handler serving the actual backend JSON file; the agent authors the response file, not a backend HTTP server. The frontend work is an independent `ui.txt` artifact plus a Python client, not a built React app or browser-rendered UI. The client asserts schema version 2 and the object-shaped users, then records `{"passed":true,"schema_version":"2","rendered":["Ada"]}`. Assertions check the real artifacts and shell exit/output separately from contract readiness. Fresh store instances and a fresh API session-message reader verify persisted transcripts, immutable v1, current v2, acknowledgements/readiness and completed schema-change delivery. Progress, schema-change and automatic answer retain the executing request's causal lineage; the answer also retains its reply link.

Run with `PG_TEST_URL` set to a disposable PostgreSQL database and `OVERKILL_REQUIRE_TEST_DB=1`:

```sh
go test -race ./internal/api -run '^TestCoordinationComposedFrontendBackendAcceptance$' -count=3 -v
```

Python 3 must be available on `PATH`. Set `OVERKILL_COORD_ACCEPTANCE_ARTIFACT_DIR` to an output directory to retain the actual `endpoint.json`, `ui.txt`, `client.py`, `integration.json`, both session transcripts and a contract/message evidence manifest after temporary workspace/database cleanup. The manifest also includes the durable usage journal. This provider omits token usage, and the test explicitly requires unknown usage to remain recorded; no complete token/cost accounting is claimed. The retained client URL identifies a temporary server which closes when the test finishes.

This scenario composes local provider protocol, API, worker, persistence and real tool effects. Scripted responses and synchronization gates make ordering deterministic; they do not establish autonomous model planning quality, paid-provider behavior, cross-process recovery, or the remaining COORD-08 acceptance cases. Existing busy/isolation/restart/duplicate/cancel fixtures remain separate evidence.

Contract specs have bounded schema/criteria/artifact/dependency counts and a 512 KiB serialized limit. Listing is limited to 1,000 records and 1 MiB combined output; exceeding a bound returns an explicit error, never a partial inventory labeled complete. Read known IDs individually for large schemas. Paginated metadata discovery is a remaining improvement.

## Desktop acknowledgement controls

In the Agents rail, Contracts lists shared interfaces for the selected registered chat. Expand a revision and choose **Acknowledge revision N** to record that chat's agreement. The IPC bridge fixes the action and derives the peer from the chat; it accepts only session, contract ID and positive integer revision. Acknowledgement does not mark implementation or acceptance checks complete.

The button disables while saving and after confirmation. Polling preserves expanded cards and pending/error state. Changing chats or revisions discards late responses; a new revision requires fresh agreement. **Refresh agreements** reloads the current revision after a conflict. Failed requests remain visible and can be retried explicitly. Existing acknowledgements are recovered from server readiness on reopening.

Source acceptance includes real native Electron/backend/database clicks for two revisions and a server-rejected stale revision, plus unit cases for duplicate clicks, late responses, failure/retry and list recovery. See `.cache/desktop-contract-ack-native-focused.log` and `desktop/test/workspace.test.cjs`. The desktop workspace fixture uses disposable credentials; encrypted settings and actual Keychain behavior have separate native tests. These controls are source changes and are not included in the previously accepted frozen development DMG.

Final source desktop acceptance passes **32 tests, zero skips/failures**, including the real encrypted-settings/native relaunch case and the workspace two-revision acknowledgement scenario (`.cache/desktop-contract-ack-full-native.log`). Visual review of `.cache/desktop-contract-inspector.png` confirms the revision, saved acknowledgement and agreement-versus-checks wording. The frozen DMG remains unchanged.

## Desktop contract authoring

The Contracts tab offers **New contract** for a registered chat and **Edit contract** on that chat's own agreements. The editor collects the interface, schema version and JSON response schema, acceptance criteria, owned relative paths, named artifacts and pinned dependencies. Existing artifact metadata, task/worktree references and dependency pins survive editing. A schema/interface change requires a new version and an explicit compatibility description. Choosing a newer dependency revision is deliberate; polling never changes an open draft.

Publishing uses fixed create/update IPC operations, a bounded allowlist of spec fields, server-derived workspace/owner and the revision captured when the draft opened. Backend ownership, path, dependency and cycle validation remain authoritative. A stale revision cannot overwrite current state. Submit is serialized, late responses cannot affect another chat/draft, and errors retain editable draft text. Failed/ambiguous saves disable further publishing until closing and inspecting a refreshed contract list; creation has no idempotency key and is never automatically retried. The desktop request limit is 60 KiB of spec JSON. This adds authoring, not task graph scheduling or acceptance execution.

Final native visual follow-up also passes (`.cache/instructions-evaluation-native-final.log`): the 850px header controls no longer overlap, and the actual instruction/review/contract scenario still passes after the layout correction. Screenshot: `.cache/desktop-workspace-native-final.png`.
