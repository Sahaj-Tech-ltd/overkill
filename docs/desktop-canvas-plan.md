# Native desktop canvas integration plan — 2026-10-22

The current durable canvas is reachable through `overkill web`, not the native
desktop JSON-RPC server. The desktop must render inside its existing workspace
window and use its private backend/database; opening the browser SPA is not the
requested product behavior. This document records the original proposal and the implemented source
checkpoints below.

## Verified current architecture

- `internal/web/canvas_store.go` owns the PostgreSQL `canvas_states` document and
  session FK, committed revisions and atomic transitions. `canvas_model.go` and
  `canvas_validation.go` define the custom `overkill.canvas.v1` catalog. The
  browser HTTP handlers are adapters around this store.
- `cmd/overkill/web.go` explicitly migrates and supplies `web.NewCanvasStore`.
  `cmd/overkill/desktop_cmd.go` starts the managed local database, runs central
  migration and session initialization, constructs runtime services and starts
  `internal/api.Server`. It currently supplies no canvas store.
- `internal/api.ServerConfig.DB` carries the actual shared pool, and API dispatch
  explicitly enumerates JSON-RPC methods. API startup does not serve browser
  `/api/canvas` routes. `desktop/src/backend.cjs` privately holds API credentials
  and exposes application operations through bounded `Backend.call` responses.
- `desktop/src/preload.cjs` exposes named IPC calls; there is deliberately no
  arbitrary RPC bridge. `main.cjs` accepts IPC only from its owned top-level
  document. Its request allowlist explicitly enumerates renderer assets, so
  adding a script without updating that list would break loading.
- `desktop/src/renderer.js` has a selected `current` session and independently
  scoped task/check controllers. Its event bridge currently forwards backend
  state changes and the active conversation stream, not canvas events.
- `scripts/package-desktop-dev.mjs` copies `desktop/src` and inventories its files.
  It does not copy `internal/web/static`. Shared canvas assets must be supplied
  explicitly and their byte identity verified; manually maintained duplicate
  renderers would drift.

Dependency evidence: `.cache/desktop-canvas-web-dependencies.txt` records actual
`go list -deps ./internal/web`. It includes `agent`, `tools`, `acp` and `session`,
but does not include `api`. Therefore **api → web is currently acyclic**. In
contrast, **tools → web → agent → tools would cycle**. `internal/acp/server.go`
explicitly avoids importing agent for the analogous reason. No tool or ACP
package should acquire a web dependency to access canvas state.

## Proposed minimal backend/API contract

The first native integration can reuse the existing `*web.CanvasStore` through an
explicit `ServerConfig.Canvas` dependency and dedicated handlers; this adds no
cycle and does not duplicate the model or persistence code. Initialize/migrate
the store in runtime service startup after sessions exist, propagate failures,
and pass the same instance into API configuration. Do not silently create a
second pool or fall back to transient state.

Proposed authenticated JSON-RPC methods:

| Method | Exact parameters | Result | Purpose |
| --- | --- | --- | --- |
| `canvas.state` | `{session_id}` | `{state: CanvasState}` | Read a persisted existing session; no agent creation |
| `canvas.apply` | `{session_id, jsonl}` | `{state: CanvasState}` | Apply exactly one strict bounded custom-catalog mutation |
| `canvas.reset` | `{session_id}` | `{state: CanvasState}` | Atomically clear the session canvas, preserving revision monotonicity |

All parameters use exact case-sensitive keys, reject duplicates/unknown fields,
and consume exactly one JSON object. Read/mutation errors preserve the existing
store's not-found/invalid/unavailable distinctions without leaking SQL. Session
IDs are validated and checked against the session store. The application token
continues to authorize its local session collection; adding `session_id` does
not create a new cross-session ownership model. No caller-supplied database,
project, filesystem, Chrome executable, browser URL, renderer or script fields
are accepted.

Initially only state reading is exposed to the renderer. Apply/reset are backend
application interfaces for subsequent agent tools and explicit application
fixtures, not a generic JSON editor hidden in the product. If canvas tools are
added concurrently, define their small bound backend interface/callback in the
tools package and inject the real store from runtime setup. That preserves the
current dependency graph. A later extraction of the model/store/strict decoder
into transport-free `internal/canvas` is a sensible ownership improvement, but
should preserve exact state, validation and schema rather than reimplement them.
If that extraction is selected now, keep web aliases/adapters and move the
model's private-function tests with it; flows also currently use the strict JSON
decoder and must retain its validation guarantees.

## Proposed typed desktop bridge and view

Expose only these new renderer methods:

- `window.overkill.canvasState({sessionID})` → `{state}` through
  `desktop:canvas:state`, mapped only to `canvas.state`.
- `window.overkill.saveCanvasSnapshot({sessionID, surfaceID, revision})` →
  `{saved, cancelled?, sessionID, surfaceID, revision}` through
  `desktop:canvas:saveSnapshot`. No arbitrary output path or RPC method input.

A new narrow `desktop/src/canvas.cjs` validator should require exact keys, the
existing session-ID syntax, a valid bounded surface ID and a safe nonnegative
integer revision. Main IPC must retain the existing trusted-document check and
bound backend lifetime. Canvas operations remain independent of peer
registration; an ordinary saved chat can have a canvas.

Add a Canvas workspace tab/panel in `desktop/src/index.html`, driven by a new
`canvas-panel.js` controller with a selected-session callback. Reuse the exact
`OverkillCanvas.render` and `canvas.css` sources from the browser implementation.
A deterministic preparation step can copy the two canonical files into
`desktop/src`, with a required unit/build check comparing hashes; packaging then
copies/inventories the same files as every other desktop asset. Update main's
exact asset allowlist, CSP/local document references and load order.

The controller refreshes on panel open, selected-chat change, successful backend
reconnect and completion of a relevant turn. Because the desktop backend has no
canvas event channel, bounded polling while this panel is visible is an honest
minimal live-update mechanism. Do not claim WebSocket event delivery through an
unrelated bridge. Pause polling when hidden, disconnected or no chat is selected.
Use a generation plus session ID and backend epoch on each request; late replies
must never populate a newly selected chat. Coalesce concurrent refreshes, retain
a visible error/loading state, and clear snapshot eligibility when stale. Reset
surface selection when its surface disappears.

## Native PNG capture without external browser dependencies

The desktop already bundles Electron's Chromium runtime. Requiring the web
snapshot handler's separately installed Chrome would make native export fail on
a fresh Mac despite a working desktop. Use one short-lived owned hidden
BrowserWindow to capture a **frozen committed state**, using the exact shared
renderer/CSS:

1. Main receives the typed save request, reads `canvas.state` itself, validates
   session/surface identity and exact displayed revision, and rejects a conflict
   rather than substituting a newer revision. Require a rendering surface.
2. Create a hidden sandboxed, context-isolated, node-disabled capture window with
   no renderer API bridge, no permissions/navigation/popups and no network
   access. Load only a fixed bundled snapshot document/assets. Transfer the
   bounded state as data through a fixed trusted initialization call; no caller
   expression, URL or HTML is accepted. The capture document renders its explicit
   surface and signals readiness/failure.
3. Capture a bounded fixed viewport (the web bounds are 240–1920 by 160–1440),
   check revision/readiness and obtain a real PNG through Electron capturePage.
   A snapshot window must never reuse or navigate the user's main webContents.
4. Invoke the native Save dialog with a generated `canvas.png` suggestion. Write
   only its selected path; cancellation is a clean result. Return metadata to the
   renderer, never backend credentials. The revision returned is the one captured.
5. Close the exact owned capture window in finally. Limit concurrency to one,
   apply a bounded timeout and cancel on app shutdown/backend restart. If the
   main selected chat changes while export is pending, either cancel before save
   or retain explicit session-labelled export metadata; never attach success to
   the new chat. The UI disables duplicate exports while one is pending.

This is a distinct native adapter over the same document and renderer, not a
second canvas schema or a screenshot of whichever chat happens to be onscreen.

## Specific implementation files

- Backend: new `internal/api/canvas_handlers.go` and tests; narrow additions to
  `internal/api/server.go` dispatch/config; runtime initialization and
  `APIConfig` in `cmd/overkill/runtime_setup.go` (and startup tests).
- Main/bridge: new `desktop/src/canvas.cjs`, optional
  `canvas-snapshot.cjs`/`canvas-snapshot.html`/`canvas-snapshot.js`; narrow changes
  to `main.cjs`, `preload.cjs`, `index.html`, `renderer.js`.
- Shared assets: exact generated copies `desktop/src/canvas.js` and `canvas.css`;
  deterministic synchronization in desktop preparation/packaging, with hash
  verification. New `canvas-panel.js` contains only native controller behavior.
- Tests: new API real-PostgreSQL fixtures; `desktop/test/canvas.test.cjs` for
  strict bridge/assets; dedicated `desktop/test/canvas-native.test.cjs` using the
  existing disposable fake-provider/settings fixture. Keep native real-Keychain
  acceptance and mock-storage installed smoke labels distinct.

## Acceptance required before claiming native canvas support

1. Actual authenticated JSON-RPC → exact shared store → PostgreSQL round-trip,
   strict invalid mutation rollback, concurrent revision behavior, session
   isolation, deletion cascade and reconstruction after restart. No provider call
   is required merely to read a canvas.
2. Real desktop window selects a session, receives persisted state through named
   IPC and renders expected nodes/bindings. A second session with the same surface
   ID remains isolated. Late first-session state/export results cannot alter it.
   Agent writes or application mutation fixtures refresh the open native panel.
3. Real Electron export decodes as PNG with declared dimensions, changes with
   accepted content and reports its committed revision. Missing/nonrendering
   surfaces, stale displayed revision, save cancellation, concurrent capture,
   backend death and shutdown clean up owned capture windows.
4. The preload still exposes no generic RPC, token, process/require, arbitrary
   filesystem path or browser target. Malicious state is text and cannot initiate
   network/script/HTML execution in either display or capture.
5. Shared renderer/CSS hashes match canonical sources before packaging. The new
   installed DMG opens the canvas inside its native workspace and exports using
   bundled Electron without an external Chrome installation. Installed fake-only
   smoke cannot establish real Keychain acceptance or distribution readiness.

The browser canvas work is evidence of a shared backend foundation. It is not
proof that this native desktop contract has been implemented or accepted.

## Implemented source checkpoint

The native product path above is now implemented in source. `runtimeServices`
initializes the canonical session schema on its owned pool before canvas FK
migration, supplies the same `CanvasStore` through `APIConfig`, and returns
startup failures. JSON-RPC exposes the three fixed methods and returns `{state}`.
Parameter decoding rejects duplicate/unknown/case-folded keys, nonstring fields,
trailing values and oversized updates. Mutations use session admission; reads
inspect committed state without creating an agent or waiting for turn admission.

The desktop adds only named `canvasState` and `saveCanvasSnapshot` bridge methods.
The native panel uses the shared custom catalog renderer, selected-chat generation
checks, at most two in-flight state reads across generations, visible-only
2.5-second polling and refresh on reconnect/turn completion. It displays loading,
empty/error/revision state, surface choice and Save PNG. Producer agent tools are
not included in this checkpoint; application mutation uses the fixed authenticated
RPC methods.

PNG export rereads committed state and rejects a stale displayed revision. It
renders into a separate hidden, sandboxed, node-disabled and network-disabled
Electron window with no preload/application bridge. The window loads only fixed
local renderer/CSS/capture assets. The fixed initialization expression transfers
bounded state as base64 JSON data. Export captures a 960×640 viewport and explicitly
normalizes actual image pixels to those dimensions, including on Retina displays.
One export runs at a time, a 15-second timeout and shutdown close owned windows,
and backend epochs prevent saving after disconnect/restart. The native Save
dialog selects the destination; cancellation is a clean result. Late completion
metadata never updates another selected chat.

`desktop-canvas-assets.mjs` copies canonical renderer/CSS during preparation.
Packaging refuses mismatching copies, and unit checks compare exact bytes. The
main document's exact resource allowlist includes the new shared/controller files.
No arbitrary RPC, renderer URL, script, HTML or output path is accepted through
the new bridge.

Evidence:

- `.cache/desktop-canvas-api-race.log`: actual authenticated application RPC and
  PostgreSQL, committed reads during admission, mutation blocking/rollback,
  independent-session state, store reconstruction, strict parameter rejection,
  unauthenticated ingress rejection, monotonic reset and deletion cascade.
- `.cache/desktop-canvas-native-final.log`: mandatory actual native window and
  local PostgreSQL; safe bound text, exact 960×640 decoded PNG, changed-content
  PNG hashes, stale revision rejection, cancellation, two-session isolation,
  delayed stale reads, delayed old-chat export completion, concurrent-export
  rejection, owned capture-window cleanup and persisted canvas after actual
  app/database relaunch. The disposable fixture uses fake credentials/storage;
  it does not establish real Keychain encryption.
- `.cache/desktop-canvas-native.png`: inspected native workspace view.
- `.cache/desktop-canvas-runtime-race-final.log`: focused runtime/desktop startup
  verification, including services constructed on a fresh pool.

The development resources were rebuilt for native acceptance. This checkpoint
has not rebuilt a DMG or established installed native canvas acceptance. Existing
installed real-Keychain and distribution-readiness limitations remain unchanged.


## Session-bound model producer and export publication

The optional model tool `canvas` is now wired into session runtime construction.
Its exact input is `{action: "state" | "apply" | "reset", jsonl?: string}`;
`jsonl` is required only for apply and encodes one strict custom-catalog mutation.
The advertised schema has no session, workspace, URL, path or renderer field.
Runtime construction confirms the canonical session exists in the shared store
and captures that session ID and store pointer in an injected `CanvasHandler`.
Unpersisted or database-free runtimes do not advertise the tool. The tools package
imports neither `web` nor `api`, preserving the acyclic dependency graph.

The callback calls the shared store directly because the model turn already owns
session admission; reentering the API mutation handler would be incorrect. State
reads remain reader-safe. Apply/reset are write-like for privilege and scoped
subagent enforcement; a canvas effect has no fabricated filesystem target and
cannot be authorized by supplying a forged `path`. The existing coordination
execution guard permits valid state observation and retains its conservative
unrestricted-effect and required-agreement checks for mutations. Thus a peer's
canvas write can be blocked when other peers reserve overlapping workspace paths;
this checkpoint does not silently expand those existing effect permissions.
Known typed ACP status/await/session-list tools similarly remain observable under
file ownership constraints, while ACP submission/cancellation retain effect guards.
An arbitrary adapter merely named `acp_status` receives no exemption.

Actual controlled-provider native acceptance emits four canvas tool calls through
an admitted API model turn: create a surface, supply components, bind its data and
begin rendering. The native panel automatically refreshes to the committed result,
exports its actual PNG and restores the model-produced surface after app/database
relaunch. This is distinct from the earlier manually seeded application fixture.
The provider is controlled/local; no paid-provider compatibility is implied.

Export result feedback now has its own persistent status element so routine
state polling cannot erase saved/cancelled/error receipts. The root-owned
`canvas-save.cjs` stages PNG bytes in an exclusive same-directory temporary file,
checks backend liveness, then renames atomically. Failure/cancellation before
publication preserves an existing user file and removes the owned staging file.

Additional evidence: `.cache/canvas-producer-final-race.log` covers runtime binding,
strict tool inputs, reader/scope restrictions and typed observation guards with
actual PostgreSQL/HTTP. `.cache/canvas-producer-native-final.log` covers the native
provider/tool/store/display/export/relaunch path. `.cache/desktop-canvas-atomic-save.log`
records atomic filesystem publication acceptance. No DMG was rebuilt for this
source checkpoint, and installed Keychain/distribution readiness remain separate.
