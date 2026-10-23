# Scoped peer credential lifetime

New `overkill agent-peers configure` credentials default to **24 hours**. A trusted operator may choose `--ttl` from `1m` through `720h` (30 days). The database computes the absolute expiry using its own clock, and CLI output adds `credential_expires_at` alongside peer metadata. No secret bytes are printed. The existing exclusive private-file delivery and unconfirmed-commit handling remain in place.

```sh
overkill agent-peers configure \
  --anchor-session EXISTING_PROJECT_SESSION --session TARGET_SESSION \
  --endpoint http://127.0.0.1:8421 --capability users-schema \
  --ttl 24h --credential-out /absolute/private/new-peer-key
```

`ConfigureSessionPeerWithTTL` is the trusted Go provisioning operation. Invalid/subminute/over-30-day TTLs fail before private delivery or credential/peer-row mutation. The new nullable `coordination_credentials.expires_at` column migrates in the store's existing bounded, schema-locked transaction. Existing rows keep NULL expiry and their previous authentication behavior; migration never silently expires, deletes or replaces old keys. Legacy Go provisioning entry points retain compatibility. New CLI provisioning always uses the bounded TTL operation.

## Expiry, rotation and revocation

These are distinct operations:

- **Expiry** makes one key fail authentication at its database-clock deadline. It does not set `revoked`, disable the peer, delete correspondence or create a replacement key.
- **Rotation** privately provisions another key for the same peer. Existing keys retain their own expiry/revocation state. The replacement can retrieve the same durable idempotent receipts; changing keys does not duplicate a request. Explicitly bind the replacement private file to the chat through the existing admission-controlled `bind-acp` operation.
- **Single-key revocation** retires exactly the key read from an operator-selected private file. It does not disable the peer or revoke its replacement. The operation requires existing enabled anchor/target bindings in the same project; a key belonging to another target/project is rejected.
- **DisablePeer** disables the peer and revokes all its keys, independently of their expiry. It remains a separate trusted administrative operation.

```sh
overkill agent-peers revoke --anchor-session EXISTING_PROJECT_SESSION \
  --session TARGET_SESSION --credential-file /absolute/private/old-peer-key
```

Revocation is idempotent. Key files and expired credential rows are not automatically deleted. Private files retain their existing permissions; errors and command output do not include credential contents. Model-facing coordination tool schemas do not acquire TTL, token, credential-file or identity overrides. This feature concerns scoped peer credentials, not the application's global API/ACP token or upstream provider credentials.

## Authorization boundaries

`Store.Authenticate` rejects revoked, disabled or expired credentials. ACP ingress binds the authenticated actor and only the credential hash to a private request context. Send/reply, claim, transition/cancel and manual recovery revalidate that credential under their existing transaction locks and again at the final commit gate. These checks use `clock_timestamp()`, rather than transaction-start time. A request authenticated before expiry but held inside a database operation until after expiry rolls back its new receipt, events and accounting changes.

A mutation is authorized at those database checkpoints; expiry does not retroactively undo an already committed transaction. Read/await paths recheck scoped authentication, so a bounded observation cannot continue polling with an expired cached authentication context. An expired key cannot regain authority by supplying a valid live execution capability or known owner/message IDs. No fallback to the global ACP token occurs.

Credential expiry does **not** retroactively cancel an already-admitted local task or invalidate its execution lease. Local trusted execution may finish under that existing ownership, and a separately valid replacement key may observe/communicate for the same peer. Additional requests using the expired key are rejected. This preserves the distinction between credential lifetime, task cancellation, lease fencing and uncertain-effect recovery.

## Executed acceptance

Real isolated PostgreSQL/race fixtures cover:

- Migration from the credential table without an expiry column, with unchanged legacy hash and NULL-expiry authentication after repeated startup.
- Minimum/maximum/invalid TTLs, returned expiry matching the stored database timestamp, actual database-clock expiry, cached-context mutation/await refusal, and fresh store/server authentication after expiry.
- A real HTTP request authenticated before expiry, blocked inside its insert until the database deadline passes, then returning unauthorized with no committed message/event/task-usage row.
- All scoped ingress operations refusing an expired key, including cancellation and manual recovery; a live execution capability does not bypass expiry.
- Rotation under an active lease, replacement authentication and unchanged receipt identity; expired keys remain rejected after reconstruction while already-admitted work can finish normally.
- Actual CLI private-file provisioning with explicit/minimum and default TTLs, metadata-only output, invalid-zero refusal, single-key retirement and continued replacement/peer availability.
- Cross-project/wrong-target revocation refusal.

Fixtures shorten a stored deadline to exercise real clock boundaries quickly; the public provisioning minimum remains one minute. Existing process-restart correspondence and local controlled-provider acceptance are separate evidence. This slice does not close COORD-08: opt-in real-provider acceptance is still required. No paid provider, desktop/TUI source, installed application or frozen DMG was changed.
