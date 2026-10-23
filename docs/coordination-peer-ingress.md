# Scoped coordination on the ACP listener

The ACP server optionally mounts `/v1/coordination/` when its `Config.Coordination` is configured. The documented `overkill acp serve` path supplies the shared PostgreSQL coordination store. These routes authenticate a **scoped peer credential** through `coordination.Store.Authenticate`. The global ACP bearer for `/v1/messages` and `/v1/tasks` is not accepted as a substitute. Request bodies cannot select the sender/project or supply credentials.

The existing local registry records name, role, canonical workspace and project scope; explicitly registered repository peers also record their validated worktree/repository identity. Endpoint and supported capabilities are advertised metadata. Capabilities do not grant authorization. Local workspace scope permits peers in the same canonical workspace; repository scope requires explicit registration of each linked worktree. Advertising an endpoint never causes an automatic outbound request.

An authenticated application first registers both chats through the existing Agents UI/RPC. A trusted operator can then configure an existing identity and create a private credential:

```sh
overkill agent-peers configure \
  --anchor-session EXISTING_PROJECT_SESSION \
  --session EXISTING_TARGET_SESSION \
  --endpoint http://127.0.0.1:8421 \
  --capability users-schema \
  --credential-out /absolute/private/new-peer-credential
```

The anchor and target must have enabled persisted bindings in the same project, with matching current canonical folders/repository identity. This command cannot choose an arbitrary foreign project or create a new peer implicitly. It prints peer metadata only. The credential file must be new; exclusive creation with mode 0600 prevents overwriting an existing secret. The database stores its hash only. Failed file delivery rolls back metadata and credential creation. A commit acknowledgement error retains the private file and reports an unconfirmed commit; inspect or revoke the peer before attempting new provisioning. No automatic retry or assumption that the commit failed is made. Disabling the peer revokes all its credentials. New CLI keys default to 24 hours with bounded `--ttl` support; expired keys fail authentication and mutation checkpoints. Use `agent-peers revoke` to retire one old key without disabling its replacement. See [credential lifetime](coordination-credential-expiry.md) for migration and active-task boundaries.

`agent-peers call` is a concrete operator client for the scoped ingress. The endpoint and credential file are trusted CLI configuration, while the bounded JSON request is read from a file:

```sh
overkill agent-peers call --endpoint http://127.0.0.1:8421 \
  --credential-file /absolute/private/new-peer-credential \
  --operation send --body-file /absolute/request.json
```

For `send`, the body is `{"send":{"to":"backend","kind":"request","payload":{"question":"Is GET /users ready; what is its response schema?"},"idempotency_key":"stable-request-key"}}`. Optional conversation/task/reply/deadline/dependency/contract fields use the existing `SendInput` contract. Reuse the exact body/key after an uncertain response. Supported operations are `peers`, `send`, `reply`, `inbox`, `claim`, `status`, `await`, `cancel`, `transition`, and `recover`. Reply fixes the destination/conversation from the received message. Status returns bounded message events. Await observes for at most 25 seconds; ending observation does not cancel work. Explicit claim starts a single pending message; started work is not automatically reclaimed after a crash. Manual claim and ownership-changing transition/reply/completion refuse any existing worker lease, including expired leases. The lease check and mutation share the existing admission/peer transaction. Monotonic participant cancellation remains available during owned work and does not undo effects. Non-completing correspondence may still be sent. The credential is the same peer principal as local tools, not a separately leased external executor: simultaneous local and manual external execution is not an advertised mode. An explicitly bound same-hub runtime may send/reply during its own dispatched turn using a separate private random execution capability, whose hash and live lease/parent are revalidated in the mutation transaction. Model tool arguments cannot supply that capability or select an execution identity.

To switch interrupted work to manual inspection, first stop the local delivery runtime, then call `recover` through the operator CLI. Recovery locks the peer and lease row, waits for an in-flight renewal and refuses a live owner. Stopped/expired started work becomes blocked with an `executor_interrupted` review event; the stale lease is removed, with no automatic replay. Inspect status, then explicitly cancel the interrupted request or use the existing trusted reconciliation/review controls if replay is intended and usage gates permit it. Recovery does not certify uncertain effects or erase usage. Repeated recovery does not duplicate the blocked event. These controls provide a concrete recovery path without private database edits. These operations do not bypass dependency, contract or task-usage gates.

Ingress allows 72 KiB request bodies, 64 KiB message payloads through the existing store, 200 list/events records per query, a 1 MiB serialized response and a 30-second request deadline. Oversized combined responses fail explicitly instead of returning a partial success. The protected client rejects redirects, credentials embedded in endpoint URLs, queries/fragments and non-loopback plain HTTP. Explicit loopback HTTP resolves and validates every address before pinning the socket; HTTPS uses the existing callback public-address/DNS-pinning/TLS transport policy (port 443). Proxies are disabled. Advertised endpoints are never implicitly dialled.

`acp.Client.Coordination` and `tools.NewRemoteCoordinationTools(endpoint, scopedToken)` provide concrete clients and six agent-facing operations: `agent_send`, `agent_reply`, `agent_inbox`, `agent_task_status`, `agent_await`, and `agent_cancel`. Tool schemas contain no sender, project, URL or credential argument. The constructor fixes one application-provisioned remote identity. A registered chat can now explicitly provision these six tools against its **same hub** using the authenticated application binding described in [runtime acceptance](coordination-runtime-acceptance.md). The authenticated ACP peer ID/project/canonical workspace must exactly match its existing local binding. Unconfigured local tools retain direct-store transport. Foreign-hub identity/workspace substitution and independent remote executor ownership remain unsupported; remote workspace metadata never replaces local filesystem guards.

The durable external `/v1/tasks` protocol remains separate: its task/message/session handles and free-form display label are not coordination conversation/peer identities.

## Executed acceptance

Real isolated PostgreSQL tests exercise the actual ACP client/server with scoped credentials, all six message kinds, durable task/conversation/reply links, progress/status, reconnect and idempotent retries. Unknown/foreign/disabled peers and the global ACP bearer are rejected; redirects never receive the secret. The six remote tool adapters execute send/inbox/reply/status/await/cancel against that real ingress and reject model-supplied identity/URL/secret fields. CLI provisioning tests cover private permissions, exclusive existing-file refusal, project/folder mismatch, delivery rollback and conservative commit failure handling.

`TestCoordinationScopedCorrespondenceSurvivesActualProcessRestart` starts an independent ACP server process, sends and claims a request, records a controlled external file effect, kills the process, and starts a fresh process against the same isolated database. Identical submission returns the original started receipt; another claim fails, the effect file remains singular, and an explicit correlated reply/retry retains its identity. This proves process-persistent manual external correspondence/admission. It does not assert autonomous model execution or recovery of uncertain provider effects.

The original local controlled-provider composed acceptance separately proves independent frontend work, same-conversation backend answers and safe-turn notifications after interactive session admission releases. Neither test establishes paid-provider quality or every optional protocol integration.
