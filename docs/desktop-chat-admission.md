# Desktop chat admission and draft preservation

A real background coordinator could occupy the selected chat while its desktop composer still appeared available. Previously the renderer appended a user card and erased the draft before `desktop:send` returned. Main returned success before the actual `/stream` response arrived. An HTTP409 therefore left an unsaved user card and an empty composer even though no human provider turn ran.

The controlled pre-fix native reproduction is `.cache/chat-admission-before.log`: the exact whitespace draft assertion failed because the composer was empty. Its observed rejection snapshot contained one phantom user card and zero human provider calls. The same actual application/managed-PostgreSQL scenario now passes in `.cache/chat-admission-delayed-ipc.log`; `.cache/chat-admission-rejection.json` records the unchanged draft, zero phantom cards and zero human provider calls. The fixture also verifies the rejected text is absent from persisted history.

## Admission contract

`Backend.startStream` reserves a stream synchronously and returns separate `admission` and `completion` promises. Admission resolves only after successful HTTP headers with `text/event-stream`; rejection, invalid content type, transport failure or pre-admission cancellation rejects it. Header waiting has a 30-second bound. No admission or chat events are fabricated for an HTTP409. Existing `Backend.stream` remains a completion-oriented wrapper for direct clients/tests.

Main awaits admission rather than returning a fire-and-forget receipt. Its fixed `admitted` event includes the request ID and session ID, and is emitted before consuming any response event. Renderer retains the exact composer draft until admission, then appends the submitted user text and clears that draft once. The IPC result is an idempotent fallback. Pending request/session identity fences late failures; an old response cannot clear a newer pending request. Per-response Stop appears after admission; the stronger app-wide Stop remains available while admission is pending.

Headers attest admission, not saved history or successful completion. If an admitted stream later errors, disconnects or is cancelled, its user card and partial response remain visible with the failure. A transport failure before confirmation can be ambiguous, so the UI asks the user to review chat before resubmission. There are no automatic write retries.

## Executed acceptance

- `desktop/test/backend.test.cjs`: 8/8 passed in `.cache/chat-admission-backend.log`. Actual authenticated HTTP tests prove synchronous reservation/duplicate exclusion, HTTP409 rejection with no stream events, immediate admitted→text→done ordering, and admitted partial text retained before a disconnect error. Existing UTF-8, malformed SSE, secret redaction and shutdown cases also pass.
- `desktop/test/chat-admission-native.test.cjs`: actual source Electron/Go API/managed PostgreSQL with an actual held background coordinator. It requires preserved whitespace draft, no phantom/persisted rejected message and no human provider call; only a deliberate second click after committed coordinator completion admits the human turn once. A disposable main-process fixture delays the admission IPC reply while allowing actual stream events to finish, then verifies a newly typed draft survives the late reply and accepted history is not duplicated.
- `.cache/chat-admission-after.log`: new native admission scenario plus the complete controlled interactive frontend/backend scenario, 2/2 passed, zero skips. The latter still exercises real file/shell/integration work, clarification, per-response cancel, durable app stop/relaunch/reconnect and registry persistence.
- `.cache/chat-admission-delayed-ipc.log`: final delayed-IPC version passed separately, 1/1, zero skips. `.cache/chat-admission-native.png` captures the actual UI after rejection recovery and refresh.

The fixtures use only disposable local providers and explicit fake credential storage. They do not contact a paid provider, interact with Keychain, change an installed application, or rebuild a DMG. The workspace fixture's stream adapter was updated to the new private entrypoint while retaining its explicitly simulated revised-final-answer assertion.
