# Runtime presence

The application `coordination.peers` response now includes `presence` for every session-bound workspace peer. Registration alone yields `unknown`. Application-owned observations identify an actually loaded runtime as `ready`, `working`, `waiting_for_input` or `busy`. The UI labels these Runtime idle, Working, Waiting for input and Finishing an operation. `busy` includes admission held through durable commit or a session mutation; it does not assert that the provider is generating. Pending application questions take precedence over working.

The API service refreshes snapshots every five seconds, and peer-list reads opportunistically refresh them. Snapshots use database time and expire after twenty seconds. An unloaded runtime or an expired observation is unknown; there is no inferred offline state. Graceful service shutdown removes its observations. A crash leaves expiring rows. Two active observers for one peer produce `multiple_runtimes`, rather than claiming exclusive ownership. A presence observation is not a worker lease, execution fencing, provider health, client attachment or delivery guarantee.

No tool or RPC accepts heartbeat state or observer identity from a model/client. The service derives observations from its runtime map, admission and pending-question state. Store publication resolves persisted session bindings, excludes disabled peers and atomically replaces the observer's snapshot. The existing authenticated canonical-workspace peer inventory determines which peer IDs can be returned. Credentials, observer IDs and task payloads are absent from the public presence record.

Publication is serialized per service observer, uses a bounded snapshot and two-second database operation, and skips overlapping refreshes. Failed writes roll back to the previous committed observation, which still expires. Presence failures are recorded in service health. Observations are eventually consistent: transitions can lag a refresh, and an idle runtime may subsequently encounter provider or tool errors.

Both clients age their displayed state independently of successful RPC refreshes. Desktop updates presence leaves without rebuilding draft controls; the terminal has a one-second expiry redraw, even if a draft or detail view pauses transport polling. Timestamp expiry comparison uses the client's host clock; normal local desktop/backend deployment shares that clock. A disconnected service does not create a new successful task outcome. Older responses lacking presence remain unknown.

## Verification

Real PostgreSQL tests cover unknown registration, snapshot replacement, simultaneous observers, database-clock expiry, workspace isolation, rejected state/identity input, disabled peers and rollback after an injected publication failure. The API test runs a real provider turn, observes working/waiting/busy/idle transitions, proves a heartbeat advances without a client refresh, discards a runtime and verifies graceful shutdown. The native desktop scenario observes a blocked backend request as Working and its completion as Runtime idle. Compiled HTTP/Ink and desktop tests verify stale observations become unknown.

Logs: `.cache/runtime-presence-race.log`, `.cache/runtime-presence-fixture-race.log`, `.cache/plans-presence-tui.log`, `.cache/plans-presence-desktop-full.log`.
