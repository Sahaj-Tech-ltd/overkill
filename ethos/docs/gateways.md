# Remote Gateways

Pipe inbound messages from chat platforms into the same agent the TUI
drives. Open the TUI on your laptop, step away, message the bot from
your phone — same session, same context.

```
ethos gateway
```

## Channels

### Telegram (native)

Pure HTTP long-poll, no library deps. Create a bot via [@BotFather](https://t.me/BotFather), then:

```toml
[gateways.telegram]
enabled = true
bot_token = "123:abc..."
allowed_chats = [12345678]   # empty = any chat the bot is in
```

Or `export TELEGRAM_BOT_TOKEN=...` and skip the config.

### HTTP Bridge (WhatsApp via Baileys, Discord via discord.js, anything else)

Two endpoints. Your sidecar POSTs inbound messages to `/v1/in` and
SSE-subscribes to `/v1/out?channel=<name>` for replies.

```toml
[gateways.bridge]
enabled = true
listen  = "127.0.0.1:7799"
token   = "shared-secret"
```

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

**Wire format — outbound SSE frames:**

```json
{ "channel":"bridge:whatsapp", "chat":"+15551234567",
  "handle":"42", "kind":"post"|"update"|"final"|"error", "text":"..." }
```

Loopback-only by default — expose only behind a reverse proxy with TLS
if you need remote access.

## Cross-channel session continuity

From any channel:

| Command         | What it does                                                                  |
| --------------- | ----------------------------------------------------------------------------- |
| `/sessions`     | List recent sessions across all channels (newest first)                       |
| `/attach <id>`  | Bind this chat to a specific session id                                       |
| `/follow tui`   | Mirror whatever session the TUI is currently using — phone shadows terminal   |
| `/follow <id>`  | Pin to a specific session                                                     |
| `/unfollow`     | Clear follow mode                                                             |
| `/new`          | Mint a fresh session for this chat                                            |
| `/end`          | Clear follow but keep the binding                                             |
| `/help`         | Print the menu                                                                |

Bindings persist to `~/.ethos/gateway-sessions.json` so restarts don't
drop in-flight conversations.

## Vision (image → text)

The main agent is text-only. When a user attaches a photo on Telegram
or sends `images` through the bridge, an isolated vision model captions
it and the caption is prepended to the prompt as
`[image 1 attached by user — vision model says: <caption>]`.

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

When vision is configured, the agent gets a `vision_describe` tool. It
takes one of three sources and returns a caption:

| Field      | Behavior                                                            |
| ---------- | ------------------------------------------------------------------- |
| `url`      | Navigate the dev browser, screenshot the viewport, describe         |
| `file`     | Describe an image already on disk                                   |
| (neither)  | Screenshot the current browser viewport and describe                |
| `selector` | Restrict the screenshot to a CSS-selected element                   |
| `prompt`   | Optional steering question; default asks for an engineer-friendly recap |

So `"check that login page renders"` becomes one tool call:
`{"url": "https://app.example.com/login", "prompt": "is the form aligned?"}`.

