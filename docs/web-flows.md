# Managed browser workflow records

`overkill web` now migrates a PostgreSQL `web_task_flows` table and serves
authenticated list, create, inspect and state-change routes. Records belong to
an existing persisted session, survive server/store reconstruction, and are
deleted by the database when that session is deleted. They are separate from
the agent's suspended `flow_states` continuation snapshots, setup-provider
contributions and local task/plan files.

These routes manage workflow records. `resume` changes a record to `running`;
it does not start a provider turn, resume an agent continuation, create tasks,
execute their contents or establish that a dependency has completed. The
embedded browser client has no flow management panel yet. A caller must perform
the work and report state through the API. `controllerId` is a descriptive
label, not an authenticated agent identity.

All routes use the browser server's existing bearer token. It authorizes the
operator across this server's sessions; a `sessionKey` filter is not a separate
tenant credential. An omitted session uses the current agent session when
available. Invalid/malformed/duplicate session parameters are rejected instead
of falling back. A missing session returns 404; unconfigured flow/session storage
returns 503. Database operation failures remain errors, never successful empty
results.

| Route | Behavior |
| --- | --- |
| `POST /api/flows` | Create from `sessionKey`, `goal`, optional `controllerId`, `stateJson`, `waitJson`; returns 201 with the record and Location. |
| `GET /api/flows?sessionKey=...` | Returns `{flows, hasMore, nextOffset}`; `limit` defaults to 100 and accepts 1–200, `offset` accepts nonnegative integers with room for a next page. |
| `GET /api/flows/<id>?sessionKey=...` | Read the session-qualified record; another session's flow returns 404. |
| `POST /api/flows/<id>/tasks?sessionKey=...` | Update reported state and optional `status`: `running`, `waiting`, or `blocked`. |
| `POST /api/flows/<id>/resume?sessionKey=...` | Change a waiting/blocked record to running and clear waits/blockers. |
| `POST /api/flows/<id>/finish`, `/fail`, `/cancel` | Move an active record to `finished`, `failed`, or `cancelled`, clearing waits/blockers. |

Every mutation after creation requires a positive `expectedRevision`. Creation
starts at revision 1; a committed mutation increments it once. PostgreSQL row
locking ensures only one competing update to a revision succeeds. A stale
revision, invalid state transition or update to a terminal record returns 409;
reload before retrying. Terminal records cannot be reopened through these
routes. Repeating a create without a separate caller-managed identifier creates
another record; creation has no idempotency-key protocol.

For example, after creation:

```json
{
  "expectedRevision": 1,
  "status": "blocked",
  "currentStep": "Build the user list",
  "stateJson": "{\"tasks\":[\"backend-schema\",\"frontend-list\"]}",
  "blockedTaskId": "backend-schema",
  "blockedSummary": "Waiting for the response contract",
  "waitJson": "{\"contractRevision\":1}"
}
```

`stateJson` and `waitJson` are strings containing bounded valid JSON, stored as
JSONB and returned in PostgreSQL's canonical formatting. Each is limited to
256 KiB before storage; the whole request uses the web server's 1 MiB body
limit. Values not representable by PostgreSQL JSONB, such as escaped NUL,
return 400 without consuming a revision. Task names and dependency data in
`stateJson` are caller-reported data; this API does not enforce a task graph.

Creation without wait data starts `running`; nonempty wait data starts
`waiting`. The empty wait value is `{}`. Only `tasks` accepts `waitJson`,
`blockedTaskId` and `blockedSummary` updates. A running state cannot retain
nonempty wait/blocker data; transitioning to running clears previous values.
Waiting clears blocker fields; blocked can retain both its wait and blocker
metadata. `resume` and terminal actions reject wait/blocker fields rather than
silently accepting fields that their transitions clear. These actions can still
update `currentStep` and `stateJson`.

Request fields are case-sensitive. Unknown fields, null fields, duplicate
members and trailing JSON values are rejected. List pagination uses offsets:
concurrent inserts can move page boundaries, so refresh to reconcile. A
successful commit publishes `flow_updated` with the session and complete record
through the existing transient WebSocket bus; slow subscribers can miss events
and must refresh. Database records, not event delivery, are authoritative.

Verification: `OVERKILL_REQUIRE_TEST_DB=1 go test -race ./internal/web` with a
configured `PG_TEST_URL` runs real HTTP/PostgreSQL fixtures for restart reads,
session scoping, authentication, pagination, revision races, terminal states,
wait invariants, malformed input, JSONB failure rollback, session-deletion
cascade, and failed/concurrent migration recovery. This does not establish
automatic workflow execution or a finished browser workspace.
