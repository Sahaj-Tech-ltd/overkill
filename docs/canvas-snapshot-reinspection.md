> Implementation follow-up: WEB-02 is now repaired for the explicit custom `overkill.canvas.v1` catalog. The historical reinspection below describes the pre-implementation gap and proposed contract; current behavior and executed PostgreSQL/browser/PNG acceptance are in [canvas.md](canvas.md). This does not imply full OpenClaw/A2UI compatibility.

# Canvas snapshot reinspection — 2026-10-22

WEB-02 remains open. The original audit requires strict events plus an actual
verified canvas snapshot. Current `canvas.go` accepts validated envelopes and
publishes transient events, and deliberately returns HTTP 501 for snapshots.
`canvas_validation.go` checks duplicate keys, one action, identifiers and matching
session/surface labels. It does not validate action-specific component catalogs,
apply a data model, retain surfaces, or render. `static/app.js` has no `a2ui`
consumer. The event bus can drop messages after its 64-slot subscriber buffer
fills, so an event stream alone cannot restore a canvas after reconnect.

The existing canvas route tests prove envelope validation, identity propagation,
authorization, reset broadcasting and honest unsupported status. They do not
prove a rendered pixel, a component schema, retained state or reconnect recovery.

## Authenticated browser prerequisite repaired

The previous SPA wrote an `ethos-token` cookie and attempted a query token in
its WebSocket URL. The server accepted only an Authorization header or
`overkill-token` cookie for API/upgrade authorization. Its index handler also
independently converted query tokens into a Secure HttpOnly root cookie, which
made direct query rejection insufficient and did not reliably work over local
plain HTTP.

The SPA now exchanges a manually entered bearer through `POST /api/auth` for an
HttpOnly, SameSite=Strict session cookie scoped to `/api`. TLS connections add
Secure; plain HTTP local development can authenticate without a Secure cookie.
Issuance requires the actual bearer header, even if a valid ambient cookie
already exists, and rejects a mismatching browser Origin. Header clients may
omit Origin. WebSocket upgrades also reject a mismatching Origin. Direct bearer
clients retain their existing API behavior. Neither the index nor API accepts a
URL token as authentication, and the SPA strips the legacy parameter without
adopting it. The WebSocket URL contains no token. Logout deletes both the new
API-path cookie and the former root-path cookie. The browser keeps no bearer in
persistent script storage and clears the manual input after successful setup.

`TestBrowserCookieIssuanceRequiresBearerAndOrigin` exercises missing/wrong bearer,
cookie-only issuance rejection, Origin rejection, TLS Secure and local HTTP
cookie properties. `TestBrowserAuthenticatedWebSocket` starts the actual HTTP
server and drives the embedded SPA in real headless Chromium: URL-token refusal,
missing credentials, rejected login, manual valid login, actual cookie-authenticated
WebSocket frame delivery, reload without reentering the bearer, and logout.
Set `OVERKILL_REQUIRE_FRONTEND_TEST=1`; Node, desktop Playwright dependencies and
its Chromium runtime are required. This is browser authentication acceptance,
not canvas rendering acceptance.

## Next implementation contract

1. Pin the supported A2UI dialect and catalog explicitly. The transport currently
   mixes action names without defining versioned component semantics. Define
   strict schemas for every supported action and supported component, returning
   a clear unsupported-catalog/component error for everything else. Do not infer
   a complete catalog from acceptance of an arbitrary JSON payload.
2. Add a session/surface state store with a monotonically increasing committed
   revision. Apply each update atomically after validating the resulting tree:
   unique node IDs, existing root/child references, no cycles, finite depth/node/
   text/data limits and bounded total retained size. A failed update must consume
   no revision and publish nothing. Delete/reset must change retained state;
   reconnect must read it. PostgreSQL is already available to the web command.
3. Implement one renderer used both by the SPA and snapshot document. Use text
   nodes and an explicit component allowlist; arbitrary HTML, script, URL fetch,
   CSS or template execution must not be implicit component behavior. Render
   only the selected session/surface and committed revision. On reconnect or a
   revision gap, fetch current state instead of replaying a potentially dropped
   transient event sequence. Associate asynchronous replies with the selected
   session and reject stale responses.
4. Implement authenticated `GET /api/canvas/snapshot` with explicit session and
   surface query fields, bounded viewport and a captured revision in response
   metadata. Validate duplicate/unknown query fields. Render a frozen committed
   state through the same renderer in an isolated owned headless browser and
   capture the actual surface element as PNG. Existing chromedp dependencies
   support element screenshots; avoid sharing the agent's navigable browser
   target. No caller URL or arbitrary caller script is needed. Cancel/timeout
   must close the owned target/process. Missing renderer/runtime should remain a
   truthful unavailable error, not a fake PNG or success response.
5. Prove the real path: authenticated HTTP pushes create/update/data/begin/delete
   and reset; actual browser DOM matches the accepted graph; PNG decodes with
   correct dimensions and changes when rendered content changes; an invalid
   component cannot mutate state or execute script; restart/reconnect restores
   retained state; a concurrent update cannot mix revisions within a capture;
   two sessions using the same surface ID stay isolated. Use actual browser and
   PostgreSQL fixtures for these claims. Mock screenshot bytes are insufficient.

A first explicitly documented catalog can deliver useful real snapshots without
claiming full A2UI or upstream harness parity. WEB-02 should be reevaluated against
its actual rendering and snapshot evidence after those steps; it is not closed
by this authentication prerequisite.

## Shared renderer implementation checkpoint

The shared `static/canvas.js` renderer now declares the custom catalog
`overkill.canvas.v1`: Text, Heading, Column, Row and Divider. This is an explicit
Overkill catalog, not complete A2UI catalog compatibility. It uses DOM elements
and `textContent`, validates component properties/references/cycles, and commits
DOM changes atomically. Staged empty surfaces can exist before rendering starts.
Text bindings use nonempty RFC 6901 JSON pointers and own-property traversal;
strings, numbers, booleans and null render as text. Missing bindings show
`[Missing value]`; objects/arrays show `[Value cannot be displayed]`.

Bounds match the backend contract: at most 16 surfaces, 128 component definitions
per surface, depth 16, 16 KiB UTF-8 literal/resolved text, 64 KiB JSON data per
surface and 256 KiB serialized state. Expanded rendering is additionally capped
at 2,048 DOM component nodes per surface to bound repeated graph references.
The public `window.OverkillCanvas.render(container, state, surfaceId?)` sets
`data-canvas-revision` and `data-surface-id`; `canvas.css` is shared with snapshot
rendering. Invalid state throws without replacing an existing committed view.

The browser SPA has a Canvas panel, surface picker and authenticated PNG link.
It fetches complete committed state on panel opening, selected-chat change,
matching `a2ui` events and WebSocket reconnect. Prior-chat requests are aborted,
late replies ignored, and repeated in-flight refreshes coalesced. A data refresh
failure is visible and disables the snapshot link instead of claiming success.

`TestCanvasSharedRendererBrowser` serves the actual embedded SPA and drives real
Chromium against its authenticated WebSocket. Fixture canvas-state HTTP responses
exercise safe text/primitive/pointer binding, staged surfaces, all five component
types, malicious markup, unknown properties, malformed/cyclic graphs, UTF-8 bounds,
late selected-chat replies and actual transport reconnection. It proves frontend
behavior; its mocked state responses do not establish durable backend transitions
or actual PNG generation. Those remain separate root/backend acceptance gates.
