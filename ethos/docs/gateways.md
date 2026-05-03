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
