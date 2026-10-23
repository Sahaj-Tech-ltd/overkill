# Coordination callbacks

Callbacks deliver the existing durable, peer-filtered coordination event log to an operator-configured HTTPS receiver. They do not start, retry or duplicate an agent task. The existing `coordination.events` long-poll API remains available.

Delivery is **at least once**. A receiver may accept a request immediately before the sender crashes, loses the response, or cannot commit its acknowledgement. The retry has the same delivery ID and exact body. Receivers must verify signatures and persist a deduplication record before returning 2xx. This feature does not promise exactly-once HTTP delivery or exactly-once external side effects.

## Configure destinations

Set the environment variable in the process that starts the API/backend, then restart it:

```sh
export OVERKILL_COORDINATION_CALLBACK_DESTINATIONS='{"build-results":"https://callbacks.example.com/overkill"}'
```

An embedding application can instead set `api.ServerConfig.CallbackDestinations` to a `map[string]string`. Production leaves `CallbackTransport` nil; that field is an explicit trusted application/test transport injection, not an RPC setting.

The map has at most 64 named destinations. Names start with a lowercase letter and contain up to 64 lowercase letters, digits, underscores or hyphens. Destination URLs must use HTTPS port 443, without userinfo or fragments. Malformed configuration fails backend startup, including repeated attempts. Subscriptions accept the configured **name**, never a URL supplied by a model or message.

Each attempt resolves DNS again, rejects any non-public address in the complete answer set, and pins the socket to a validated address. TLS verifies the original hostname. Private/local/link-local, special and non-public address ranges are rejected, including mapped addresses. Proxies and redirects are disabled. Response headers are capped at 16 KiB; at most 4 KiB of the response body is drained and discarded. HTTP diagnostics, destination query strings, signing keys and API credentials are not included in delivery inspection results.

Removing a destination from operator configuration makes its next delivery a `destination_unavailable` dead letter. Restore the configuration and explicitly retry that delivery to resume it. No private-network exception is exposed through the API.

## Authenticated application controls

Use the existing authenticated JSON-RPC `/rpc` endpoint and application bearer token. These methods are application controls, not agent tools. Register the persisted chat first with `coordination.register`. The session binding supplies the peer and workspace/project identity; caller-supplied sender/project fields are rejected. Unknown sessions, foreign subscriptions and unregistered chats cannot inspect or control a subscription.

Each request uses the usual envelope:

```json
{"jsonrpc":"2.0","id":1,"method":"coordination.callback.subscribe","params":{"session_id":"REGISTERED-SESSION-UUID","destination":"build-results","after":0}}
```

| Method | Parameters in `params` | Result |
| --- | --- | --- |
| `coordination.callback.subscribe` | `session_id`, `destination`, optional nonnegative `after` (default 0) | `subscription`, one-time `signing_secret`, `signature_version: "v1"`, `delivery_guarantee: "at_least_once"` |
| `coordination.callback.list` | `session_id` | `subscriptions` with `id`, destination name, persisted `cursor`, `active`; no secrets |
| `coordination.callback.deliveries` | `session_id`, `subscription_id` | Latest 100 `deliveries`: sequence, state, attempts, next attempt time, HTTP status and a redacted failure code |
| `coordination.callback.retry` | `session_id`, `subscription_id`, positive `sequence` | `ok: true` only after a dead letter is reset durably; otherwise an error |
| `coordination.callback.cancel` | `session_id`, `subscription_id` | `ok: true` after durable revocation; no agent task is cancelled |
| `coordination.callback.rotate` | `session_id`, `subscription_id` | One-time replacement `signing_secret`, subscription ID and signature version |

The subscription signing key is 32 random bytes returned as 64 hexadecimal characters. Decode hex before using HMAC. It is independent of application and peer authentication credentials, and is never returned by ordinary list/event/model-tool responses. Store it in the receiver's secret storage. If the setup response was interrupted or the key was lost, call `rotate`; keys are not recoverable through a read endpoint.

A subscription cursor is a project event sequence filtered to that peer's correspondence. Gaps are normal because events involving only other peers are excluded. `after: 0` replays existing correspondence, including completions produced while the receiver was offline. Choose a saved sequence to start later. A cursor advances only after a 2xx response and a successful durable acknowledgement transaction.

There is at most one active subscription per peer/destination. Cancelling revokes it permanently and retains its delivery history. A new subscription to the same destination is allowed and has a new subscription ID/key/cursor. A new subscription's delivery IDs differ from the revoked one's IDs; use its `after` cursor to avoid intentionally replaying old events, or deduplicate by project/sequence across subscription generations when appropriate. Lists return at most 100 subscriptions, with active subscriptions first and recent revoked history next. Delivery inspection never returns signing material or response bodies.

## Request and signature format

Every POST uses `Content-Type: application/json` with these headers:

```text
X-Overkill-Event-ID: SUBSCRIPTION-UUID:SEQUENCE
X-Overkill-Sequence: SEQUENCE
X-Overkill-Timestamp: UNIX-SECONDS
X-Overkill-Signature: v1=LOWERCASE-HEX-HMAC-SHA256
```

The stable request body is:

```json
{"delivery_id":"SUBSCRIPTION-UUID:SEQUENCE","event":{"project_id":"workspace:...","sequence":42,"message_id":"...","revision":3,"type":"completed","payload":{"response":"done"},"created_at":"..."}}
```

Sign the following exact bytes, including the newline after the timestamp, followed immediately by the **unmodified request body bytes**:

```text
v1\n<EVENT-ID>\n<DECIMAL-SEQUENCE>\n<DECIMAL-TIMESTAMP>\n<BODY-BYTES>
```

The timestamp is fresh for each attempt; the delivery ID/body/sequence remain stable across automatic retries, restarts and explicit dead-letter retries. Do not parse and reserialize JSON before verifying it.

For example, this receiver-side Python function verifies the version, signature, five-minute freshness window and matching body identity. It performs no network requests:

```python
import hashlib, hmac, json, re, time

def verify_callback(headers, body, signing_secret_hex):
    headers = {k.lower(): v for k, v in headers.items()}
    event_id = headers['x-overkill-event-id']
    sequence = headers['x-overkill-sequence']
    timestamp = headers['x-overkill-timestamp']
    if len(body) > 80 * 1024 or not re.fullmatch(r'[1-9][0-9]*', sequence):
        raise ValueError('invalid callback')
    if not re.fullmatch(r'[0-9]+', timestamp) or abs(time.time() - int(timestamp)) > 300:
        raise ValueError('expired callback')
    secret = bytes.fromhex(signing_secret_hex)
    if len(secret) != 32:
        raise ValueError('invalid signing key')
    prefix = f'v1\n{event_id}\n{sequence}\n{timestamp}\n'.encode('utf-8')
    signature = 'v1=' + hmac.new(secret, prefix + body, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(signature, headers['x-overkill-signature']):
        raise ValueError('invalid signature')
    payload = json.loads(body)
    if payload['delivery_id'] != event_id or payload['event']['sequence'] != int(sequence):
        raise ValueError('identity mismatch')
    return event_id, payload['event']
```

After verification, use a **durable unique index on the delivery ID**. In one receiver transaction, insert that ID and record/process the event with idempotent side effects. Commit before returning 2xx. A verified duplicate should return 2xx without repeating work. For side effects outside that transaction, use a durable outbox/idempotency key rather than assuming the callback itself guarantees exactly-once effects. Do not return 2xx before durable acceptance. The signed event timestamp can be old during replay; freshness checks use the signed *attempt* timestamp header.

## Retries, recovery and shutdown

The default worker is serial with a 250 ms poll interval, a 10-second attempt deadline and a 30-second persisted lease. Subscription scheduling uses durable last-attempt ordering so an old backlog cannot monopolize delivery. There are no detached per-request workers or unbounded in-memory queues. Application shutdown cancels and joins the worker and its in-flight request; acknowledgement cleanup has a separate three-second bound.

Failures back off exponentially: 1, 2, 4, 8 and 16 seconds before the next attempt, with at most six attempts per automatic cycle. HTTP 4xx responses are immediately dead-lettered except 408 and 429; other non-2xx responses and transport failures retry within the attempt bound. Redirects are not followed. A dead letter blocks later events for that subscription until explicit retry, preserving sequence order. Delivery inspection exposes `pending`, `delivered`, `dead` and `cancelled` states. An interrupted worker releases its lease when possible; after a crash another worker can retry after lease expiry. That retry does not execute the original agent work again.

Explicit `retry` only resets an active subscription's dead letter and keeps its identity/body. Cancellation marks pending deliveries cancelled and revokes further attempts. Revocation cannot recall a request the receiver already accepted. An in-flight request observes cancellation on its owned context, with a 100 ms revocation/lease monitor.

Key rotation preserves the cursor and event identity/body, increments a persisted signing generation, releases pending leases and fences old-generation acknowledgement. An old in-flight attempt is cancelled by the same ownership monitor; it may already have reached the receiver, so deduplication is still required. Rotate is available only for an active subscription. During receiver key replacement, temporarily returning 503 is preferable to accepting unverified requests. Install the returned key promptly; an intervening 401/403 becomes an inspectable dead letter and must be retried after the new key is installed. Rotation does not reset delivery attempt counts or silently replay agent work.

All automated acceptance tests use explicitly injected local transports/TLS servers. No external callback traffic is required to run them.
