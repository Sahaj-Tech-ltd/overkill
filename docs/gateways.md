# Remote Gateways

Overkill includes messaging adapters and a generic HTTP sidecar bridge. Their
entry points do not yet share identical agent/session wiring. The matrix below
describes implementations in this checkout, not successful live delivery or a
promise of automatic terminal-to-phone context sharing.

## Capability and setup matrix

| Platform | Implementation | Setup and entry point |
|---|---|---|
| Telegram | Built-in Bot API HTTP adapter | Create a bot; configure `gateways.telegram.bot_token` or `TELEGRAM_BOT_TOKEN`, and optional `allowed_chats`. Run `overkill telegram`. |
| Discord | Built-in Discord adapter | Supply a bot token through `gateways.discord.bot_token` or `DISCORD_BOT_TOKEN`; configure guild/channel allowlists and mention policy. Run `overkill discord`. |
| Slack | Built-in Socket Mode adapter | Supply `gateways.slack.app_token` and `bot_token` (or `SLACK_APP_TOKEN` / `SLACK_BOT_TOKEN`), with appropriate workspace app permissions. Run `overkill slack`. |
| Signal | Adapter to an external signal-cli REST service | Run and register the external service, set `gateways.signal.rest_api_url`, `account` and any `auth_token`. Run `overkill signal`. The service is not bundled. |
| Matrix | Built-in Matrix Client-Server HTTP adapter | Set `gateways.matrix.homeserver_url`, `user_id`, and `access_token` or `password`. Run `overkill matrix`. |
| Mattermost | Built-in WebSocket/REST adapter | Set `gateways.mattermost.server_url`, `bot_token` and `team_name`. Run `overkill mattermost`. |
| WhatsApp paired device | Go whatsmeow implementation, using a separate SQLite device store | Set `gateways.whatsapp.enabled = true`, `backend = "whatsmeow"`, optional `allowed_from` and `whatsmeow.store_path`; run `overkill whatsapp pair` to pair. The daemon has a configured-bot startup path; `overkill whatsapp` itself is a helper command, not a running bot. |
| WhatsApp Cloud | Built-in Meta Cloud webhook/API implementation | Set `gateways.whatsapp.enabled = true`, `backend = "cloud"` and the `cloud.phone_number_id`, `access_token`, `app_secret`, `verify_token` and `listen` fields. Requires an independently configured HTTPS webhook route to the local listener. The daemon has a configured-bot startup path. |
| SMS / iMessage | Generic bridge integration points | Supply your own Twilio/BlueBubbles or equivalent sidecar and its credentials. Translate inbound events and outbound replies to the HTTP bridge format below. No ready-to-run SMS or iMessage sidecar is bundled. |
| Feishu | Not implemented | No adapter or configuration is provided in this checkout. |

Configure a model provider before starting a bot. Each standalone command runs
its own agent and router from its startup workspace. Empty platform allowlists
generally permit every sender/chat visible to that adapter; set explicit lists
when restricting access. Enabling a configuration section does not provision
platform accounts, permissions, credentials or external services.

`overkill gateway` currently **prints configured gateway status and command
suggestions, then exits**. It does not run the unified gateway. Its suggestions
are not a complete capability probe, particularly for WhatsApp and environment-only
credentials. `overkill daemon start` has a separate configured-adapter path and
also starts background automation; it is not a substitute for verified shared
API session behavior. Telegram/Discord daemon startup currently checks their
configured token fields directly.

The current `overkill serve` platform hub is constructed without a dispatcher;
do not rely on it for working platform-bot agent turns. The **API HTTP bridge**
is wired separately to persisted sessions and session-specific agents. To use
that path, set `database_url` / `DATABASE_URL`, configure the bridge below and
run `overkill serve`, leaving the other platform adapters disabled. Desktop
startup deliberately disables all gateways.

## Channels

### Telegram (native)

Pure HTTP long-poll, no library deps. Create a bot via [@BotFather](https://t.me/BotFather), then:

```toml
[gateways.telegram]
enabled = true
bot_token = "123:abc..."
allowed_chats = [12345678]   # empty = any chat the bot is in
```

Then run `overkill telegram`. The standalone command can also read
`TELEGRAM_BOT_TOKEN`; configure the allowlist in TOML. Although the command's
help mentions `TELEGRAM_ALLOWED_CHATS`, the current implementation does not
read that environment variable.

### HTTP bridge (user-supplied sidecars)

Two endpoints. Your sidecar POSTs inbound messages to `/v1/in` and
SSE-subscribes to `/v1/out?channel=<name>` for replies. The sidecar is responsible
for connecting to its platform and translating both directions; Overkill does
not call Twilio or BlueBubbles for you.

```toml
[gateways.bridge]
enabled = true
listen  = "127.0.0.1:7799"
token   = "shared-secret"
# workspace = "/absolute/path/to/project" # API bridge; defaults to startup cwd
```

Run `overkill serve` for the persisted API-session path described above, or
`overkill bridge` for the separate standalone bridge agent. These do not imply
the same durability or session sharing. Set a nonempty token and send
`Authorization: Bearer shared-secret` on both inbound and SSE requests.
An empty token disables authentication on loopback; non-loopback startup
requires a token.

**Wire format — inbound POST:**

```json
{
  "channel":  "whatsapp",
  "chat":     "+15551234567",
  "from":     "Alice",
  "text":     "what's the deploy status?",
  "is_direct": true
}
```

**Wire format — outbound SSE frame data (one example):**

```json
{ "channel":"bridge:whatsapp", "chat":"+15551234567",
  "handle":"42", "kind":"final", "text":"..." }
```

The `kind` field can be `post`, `update`, `final` or `error`. An accepted inbound
HTTP request is not proof that the model turn completed. Keep the SSE stream
connected and handle errors/reconnection in your sidecar.

Loopback-only by default — expose only behind a reverse proxy with TLS
if you need remote access.

## Cross-channel session continuity

From any channel:

| Command         | What it does                                                                  |
| --------------- | ----------------------------------------------------------------------------- |
| `/sessions`     | List this router's recent channel bindings (newest first), not all database sessions |
| `/attach <id>`  | Bind this chat to a specific session id                                       |
| `/follow tui`   | Resolve to this dispatcher's current agent session when available; see limits below |
| `/follow <id>`  | Pin to a specific session                                                     |
| `/unfollow`     | Clear follow mode                                                             |
| `/new`          | Mint a fresh session for this chat                                            |
| `/end`          | Clear follow but keep the binding                                             |
| `/help`         | Print the menu                                                                |

Bindings persist to `gateway-router.json` in the configuration root (normally
`~/.overkill`). This preserves routing metadata, not in-flight model work or
conversation history. The API bridge stores conversation history in PostgreSQL;
its configured `workspace` is assigned to newly created sessions. The standalone
bridge/platform commands use a separate agent construction path and do not
automatically restore the TUI's persisted context.

`/follow tui` reads the current session ID exposed by that dispatcher's agent;
it is not a cross-process terminal subscription. Do not assume a separately
started TUI and bot share live session state merely because their router files
have the same path. Some API bridge commands, including retry, fork, Git
snapshots/rollback and history export, explicitly return unsupported; use the
appropriate API or resend the message.

## Vision (image → text)

The gateway dispatcher has an optional image-captioning path. When a vision
describer is actually wired to that dispatcher, attached images are captioned
and the caption is prepended to the prompt as
`[image 1 attached by user — vision model says: <caption>]`.

The standalone Telegram/bridge and API bridge constructors currently do not
assign this dispatcher dependency. A `[vision]` configuration alone does not
prove gateway image understanding; an unwired dispatcher adds a placeholder.
The agent's separate `vision_describe` tool has its own runtime dependency.

```toml
[vision]
enabled  = true
provider = "anthropic"
model    = "claude-sonnet-4-5-20250929"
# api_key = "sk-ant-..."   # falls back to ANTHROPIC_API_KEY
```

Bridge sidecars send images as base64:

```json
{
  "channel": "whatsapp", "chat": "+15551234", "from": "Alice",
  "text": "what error is this?",
  "images": [{ "mime": "image/jpeg", "data": "<base64>" }]
}
```

## `vision_describe` tool

The shared CLI/API runtime registers `vision_describe` when it has built a
vision describer with credentials. Standalone gateway agents do not obtain it
merely by setting `[vision]`. Browser sources additionally need the browser
runtime. The tool takes these sources and returns a caption:

| Field      | Behavior                                                            |
| ---------- | ------------------------------------------------------------------- |
| `url`      | Navigate the dev browser, screenshot the viewport, describe         |
| `file`     | Describe an image already on disk                                   |
| (neither)  | Screenshot the current browser viewport and describe                |
| `selector` | Restrict the screenshot to a CSS-selected element                   |
| `prompt`   | Optional steering question; default asks for an engineer-friendly recap |

So `"check that login page renders"` becomes one tool call:
`{"url": "https://app.example.com/login", "prompt": "is the form aligned?"}`.

