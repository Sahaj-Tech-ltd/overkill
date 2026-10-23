# Durable browser canvas and PNG snapshots

The web workspace now has a Canvas panel backed by PostgreSQL. It renders a deliberately explicit `overkill.canvas.v1` catalog. This catalog reuses the existing event action names; it is not OpenClaw A2UI v0.8/v0.9 compatibility or arbitrary hosted HTML.

Start the web command with the ordinary configured PostgreSQL/provider setup. Canvas tables are migrated after sessions and before serving. Open a chat and select **Canvas**. The panel reloads committed state when the chat changes, after a canvas event, or after WebSocket reconnect. Stale replies from a previously selected chat cannot replace the current view. It offers a surface picker and PNG download for surfaces that have begun rendering.

Snapshots need an installed Chromium browser. The trusted administrator setting `[browser].chrome_path` selects its executable; otherwise chromedp uses normal executable discovery. Browser tool enablement and navigable browser targets are separate: a snapshot gets its own temporary browser process and never receives provider/API credentials. Missing browser availability returns HTTP 503 with an actionable message. No browser download or executable is supplied by a canvas payload.

## API

All routes require the existing bearer or authenticated browser session cookie. Session IDs must refer to actual persisted sessions. The shared web token authorizes the workspace; this does not introduce per-peer credentials or a new multi-user authorization boundary.

- `POST /api/canvas/push`: exactly one JSON object with `sessionId`, optional matching `surfaceId`, and `jsonl` containing exactly one event object. `sessionId` can be omitted only when the configured agent supplies it.
- `GET /api/canvas/state?sessionId=...`: returns `{sessionId, revision, surfaces}` from one committed database snapshot. An existing session without canvas data has revision 0 and an empty surface array. Unknown sessions return 404.
- `POST /api/canvas/reset`: `{sessionId}` clears this session's surfaces and increments its revision.
- `GET /api/canvas/snapshot?sessionId=...&surfaceId=...`: captures a frozen committed surface as `image/png`. Optional decimal `width` 240–1920 and `height` 160–1440 default to 960×640. Unknown, duplicate and malformed query fields are rejected. `X-Canvas-Revision` identifies the captured state. The PNG uses the selected viewport and clips overflowing content; it is not an unbounded full-page capture. A surface without `beginRendering` returns 409, a missing surface/session 404, and exhausted capture slots 429. Responses use `Cache-Control: no-store`.

Each successful mutation is serialized by its existing session row lock and commits one revision before publishing an event. Multiple store instances coordinate through PostgreSQL. Failed validation consumes no revision and publishes no success event. Session deletion cascades its canvas row. Revisions are nonnegative JavaScript-safe integers; exhaustion fails without discarding data. Reset does not reset revision numbering.

## Catalog and events

The following JSON values are the strings inside `jsonl`, rather than additional API envelope fields:

```json
{"createSurface":{"surfaceId":"build","catalog":"overkill.canvas.v1"}}
```

```json
{"surfaceUpdate":{"surfaceId":"build","components":[{"id":"root","type":"Column","children":["title","status"]},{"id":"title","type":"Heading","text":"Agent workspace"},{"id":"status","type":"Text","textPath":"/status"}]}}
```

```json
{"dataModelUpdate":{"surfaceId":"build","contents":{"status":"Backend ready"}}}
```

```json
{"beginRendering":{"surfaceId":"build","root":"root"}}
```

```json
{"deleteSurface":{"surfaceId":"build"}}
```

`surfaceUpdate` replaces the entire component array; `dataModelUpdate` replaces the entire data object. Neither is a patch or merge operation. A new surface is staged with no root and no rendering until `beginRendering`. Already-rendering updates must retain a valid root.

Supported components:

| Type | Exact properties beyond `id` and `type` |
| --- | --- |
| Text, Heading | Exactly one of `text` (string) or `textPath` (RFC6901 pointer beginning with `/`) |
| Column, Row | `children`: explicit array of component IDs |
| Divider | None |

Bindings display scalar values as text. Missing values show `[Missing value]`; objects/arrays show `[Value cannot be displayed]`. No component can provide HTML, JavaScript, CSS, a caller URL, actions or network requests. The same embedded JavaScript renderer and CSS serve the panel and isolated snapshot document. Snapshot CSP denies external resources, navigation bases and form actions. This is a bounded rendering boundary, not a general operating-system sandbox.

## Limits

The backend rejects duplicate/unknown/case-aliased fields, unsupported catalog/components, missing child/root references, cycles including unreachable cycles, and invalid replacement states. Limits: 16 surfaces/session, 128 components/surface, graph depth 16, 2,048 expanded rendered nodes/surface, 16 KiB UTF-8 per literal/resolved text, 64 KiB serialized data/surface and 256 KiB serialized state/session. Identifier/pointer bounds are 256 bytes; identifiers reject blank/control text. JSON decoding has nesting limit 64, including the final stored-state wrapper, so accepted updates remain reloadable.

Data numbers use finite IEEE754 semantics within ±9,007,199,254,740,991. Fractional input rounds to its nearest float64 representation; larger or nonfinite values fail before commit. Use strings for decimal/exact values that must not round. SQL stores normalized numeric JSON, not arbitrary big integers.

Migration/read/mutation work has a ten-second caller-derived context. Capture has a further fifteen-second limit, at most two simultaneous captures per server, and is canceled by request cancellation or server shutdown. Owned browser startup is reaped on shutdown. State migration uses a transaction and schema-specific advisory lock; it does not establish a global application migration transaction or every historical schema's compatibility.

## Executed evidence

Actual PostgreSQL tests cover concurrent migration/updates, reconstructing stores over durable state, two-session isolation, invalid-update revision preservation, maximum-state rollback, session cascade, safe numeric/revision bounds, and nesting-boundary readability. Actual Chromium tests cover the shared DOM renderer, all five components, safe text/bindings, invalid graph rejection, stale-session response isolation and reconnect refresh.

The integrated browser test uses the real PostgreSQL API, actual WebSocket notifications and the real PNG route: create/read/render/update, injection kept as text, invalid update rollback, separate sessions with the same surface ID, page reload, delete and reset. Actual PNG decoding verifies 640×360 output and content changes. A writer continues during capture; PNG bytes match the state identified by the returned revision. A separate owned-process fixture proves shutdown cancels/reaps browser startup; that fixture is not presented as PNG rendering evidence.

Logs: `.cache/canvas-durable-final.jsonl`, `.cache/canvas-actual-browser-e2e.log`, `.cache/canvas-snapshot-first.log`, `.cache/canvas-snapshot-shutdown.log`, `.cache/canvas-shared-renderer-browser-final.log`. Root visually inspected `.cache/canvas-snapshot-proof.png` and the integrated workspace screenshot `.cache/canvas-workspace-proof.png`. The final mandatory web race suite passed 107 tests/subtests with zero skips in `.cache/canvas-full-web.jsonl`. The subsequent whole-repository race suite passed 107 packages (eight no-test packages) in `.cache/canvas-full-race.log`; all 64 desktop checks passed with zero skips after rebuilding resources in `.cache/canvas-desktop-tests.log`. Vet and Windows cross-compilation passed in `.cache/canvas-full-vet.log` and `.cache/canvas-windows.log`. Windows execution and broader release acceptance remain unverified.

No full A2UI/OpenClaw parity, HTML widget hosting, native-device nodes, browser actions, forms/media/catalog expansion, paid-provider production, minimum-OS or release-package acceptance is implied. The existing DMG is frozen and excludes this source addition.
