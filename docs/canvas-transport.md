# Canvas transport status

The browser backend exposes authenticated `POST /api/canvas/push` and
`POST /api/canvas/reset` endpoints. These publish transient WebSocket events;
the embedded SPA currently has no A2UI renderer. There is no retained canvas,
replay after reconnect, rasterization, or snapshot backend. Push acceptance
means an event was validated and offered to connected subscribers, not rendered.
The existing bounded event bus can drop events for slow subscribers.

Push accepts an object containing string fields `sessionId`, optional
`surfaceId`, and `jsonl`. If `sessionId` is omitted or empty, the current
agent session is used when available; otherwise an explicit session is required.
Session identifiers are routing labels under the web server's existing shared
bearer-token authorization, not per-session ownership credentials.

`jsonl` must contain exactly one JSON object with exactly one of
`surfaceUpdate`, `dataModelUpdate`, `beginRendering`, `createSurface`, or
`deleteSurface`. Its value must be an object containing a nonempty string
`surfaceId`. The outer `surfaceId`, when supplied, must agree. A payload
`sessionId`, when supplied, must agree with the resolved request session.
The emitted event always includes the resolved surface and session identities.
This validates the transport envelope and identities, not action-specific
component catalogs or renderer semantics.

All request field names are case-sensitive and unknown fields are rejected.
Duplicate object members at any depth (including escaped equivalent names),
trailing JSON values, nonobject envelopes, and null/nonstring request fields
are rejected before an event is published. Session and surface IDs must be
nonblank, at most 256 bytes, have no leading/trailing whitespace, and contain
no ASCII control characters.

Reset accepts only a string `sessionId`, with the same fallback rule, and
publishes the existing `{"action":"reset"}` event for that session. It does
not claim to delete persisted surfaces, since none are stored.

`GET /api/canvas/snapshot` returns HTTP **501** with
`status: "unsupported"` and `code: "canvas_renderer_unavailable"`. It produces
no image. Actual rendering, durable/replayable surface state, and a verified
PNG capture remain work required for canvas feature parity.

Verification: `go test -race ./internal/web -run Canvas -count=1` exercises the
real HTTP routes, authentication, strict input validation, identity propagation,
reset broadcasts, rejection without bus publication, and honest snapshot status.
It does not prove rendered or captured output.
