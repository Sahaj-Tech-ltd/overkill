# Controlled local interactive coding acceptance

This receipt addresses the original `CODE_AUDIT_TODO.md` VALIDATE-02 and the local portion of COORD-08. It exercises the real source Electron application, authenticated Go API, managed PostgreSQL, model tool catalogue, filesystem/shell tools, durable coordination and rendered frontend. Its HTTP model provider is a deterministic local fixture with disposable fake credentials. It does not establish paid-provider compatibility, actual Keychain acceptance, installed-DMG acceptance, or provider billing truth.

## Executable gates

Run `scripts/verify-controlled-coding.py` with an explicit localhost `PG_TEST_URL`, prepared desktop resources and the desired Go/Node executable paths. The runner requires all 17 named real-PostgreSQL race tests and the selected native/unit fixtures to pass without skips. It deliberately excludes the separate real-Keychain `native.test.cjs` gate. The executed combined receipt is `.cache/controlled-coding-acceptance/receipt.json`; detailed logs are `coordination.jsonl` and `native.tap` in the same directory. The combined run passed 17 coordination cases and 10 desktop cases with zero skips. `.cache/interactive-desktop-unit.log` additionally records 24 desktop unit cases passing.

The final focused native run passed in 12.43 seconds with zero skips after adding post-relaunch registry assertions and waiting for committed coordination completion before the next human turn. The focused native test is `desktop/test/interactive-coding-native.test.cjs`. Its evidence is `.cache/interactive-coding-native.log`, `.cache/interactive-coding-artifacts/receipt.json` and the actual authored files in that artifact directory. The fixture uses linked Git worktrees, a disposable profile and real managed PostgreSQL; cleanup owns its server, application, browser and temporary paths. Test-only credential/RPC adapters live under `desktop/test/fixtures/interactive.cjs` and do not enter the production renderer or packaged app.

## Interactive clauses

| Original clause | Executed assertion |
| --- | --- |
| Onboarding and provider selection | Choose Custom provider/model/base URL and disposable key through the actual settings dialog, then connect. The provider checks the incoming bearer and advertised `ask_user` parameter schema. |
| Chat streaming | Hold the final provider SSE chunk until the actual desktop transcript renders the first partial text. |
| Bounded shell, file edit and verification feedback | Actual model `fs` calls write server/frontend/build/verification files within acknowledged ownership contracts; bounded shell calls run Node syntax checking, build and integration assertions. Their outputs reach persisted tool history. |
| Clarification | Actual provider tool call opens the desktop question dialog; choose a field set. A second question rejects the previous immutable question ID, remains open, then records explicit cancellation. Unit tests cover draft preservation and stale asynchronous replies. |
| Per-response cancellation | Stop an active held response, observe its provider connection close, and successfully start another response in the same chat. |
| Emergency stop | Stop concurrent frontend/backend work plus a queued request through the app-owned process stop. Both provider transports close, managed PostgreSQL exits, mutation admission closes and queued execution stays at zero. Earlier effects are retained. |
| Persistence and restart | Close and relaunch the whole app with the same profile: the persisted stop marker prevents backend/queued work. Only explicit settings reconnect clears the marker and permits the queued request once. Persisted histories and task usage remain available. |
| Session switching and current registry | Switch backend/frontend chats after reconnect; require current authenticated peer discovery and settled frontend identity/backend card, independently of saved correspondence/history. |

The stop marker is app-owned and fails closed on malformed/read errors. Stop still drains the backend if marker persistence fails, and reports that durability failure. Explicit reconnect is a deliberate permission for queued work to resume. External agents/apps are outside this stop scope; earlier file/network effects cannot be undone.

## Two-agent coding clauses

The frontend requests a users endpoint from the registered backend. Backend authors `backend.cjs` and schema-1 data, publishes a contract and progress. The frontend builds production HTML against revision 1 while the backend request is still `started` (asserted from real task state). Backend changes the users schema to revision 2, sends `contract_changed`, and completes. Frontend rereads/acknowledges revision 2, edits its implementation, rebuilds and runs a real HTTP integration check. A separate Chromium instance renders the authored frontend served by the authored backend, asserting `Schema 2`, `Ada · Engineer`, and an empty error element.

The fixture launches the model-authored server to own its bounded process lifecycle; it does not substitute a test-authored endpoint implementation. The model writes and syntax-checks that server. Actual outputs include `backend.cjs`, `users.json`, `app.js`, `build.cjs`, compiled `index.html`, `verification.cjs` and successful `verification.json`. The screenshots include `.cache/interactive-authored-users-ui.png` and `.cache/interactive-coding-native.png`.

The 17 named race cases in the runner cover automatic delivery, durable identity, server-restart event replay, authenticated/signed callbacks, duplicate delivery identity across an accepted-before-crash boundary, callback backoff/dead-letter/retry, cancellation/shutdown, lost-secret rotation and revocation, busy recipients, execution cancellation, expired worker fencing, concurrent idempotent sends, credential/project isolation, dependency failure/deadline handling and durable progress/reply replay. The native fixture separately proves whole-app stop/relaunch/reconnect and rejection of the previous launch's credential at the new backend.

## Evidence limits and closure proposal

The local VALIDATE-02 clauses are exercised. COORD-08's local coding scenario and enumerated supported failure boundaries are exercised; its explicit opt-in real-provider run remains pending. Time-expiring peer credentials are not implemented: disabled/revoked/rotated credentials, expired worker leases and prior-launch credential rejection are distinct exercised boundaries and must not be presented as peer-token TTL acceptance.

The recorded controlled run reports input/output token counts and retains unknown pricing (for example, the receipt recorded 380 input tokens, 95 output tokens and 19 unknown-price samples). A zero estimated dollar field with unknown pricing does not mean free service or a verified invoice. These local fixtures make no paid upstream calls and do not modify the installed app or request Keychain permission. Source production hashes are retained in the combined receipt; focused artifact-quality reruns do not broaden that scope.
