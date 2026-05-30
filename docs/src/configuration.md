# Configuration

Config lives at `~/.overkill/config.toml` (or `%LOCALAPPDATA%\overkill\config.toml` on Windows).

## Example

```toml
[agent]
default_model = "anthropic/claude-sonnet-4-5"
max_tokens    = 8192
temperature   = 0.2

[providers.anthropic]
type    = "anthropic"
api_key = "${ANTHROPIC_API_KEY}"

[providers.openai]
type    = "openai"
api_key = "${OPENAI_API_KEY}"

[providers.ollama]
type     = "ollama"
base_url = "http://localhost:11434"

[sync]
backend = "s3"
bucket  = "my-overkill-sessions"
region  = "us-east-1"

[acp]
enabled = true
addr    = "127.0.0.1:7777"

[mcp.servers.fs]
command = "npx"
args    = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]

[ui]
theme = "catppuccin-mocha"
```

## Config sections

| Section | Keys |
|---|---|
| `agent` | `default_model`, `max_tokens`, `temperature` |
| `providers.<name>` | `type`, `api_key`, `base_url` |
| `sync` | `backend` (`s3`, `git`, `file`), `bucket`, `region` |
| `acp` | `enabled`, `addr` |
| `mcp.servers.<name>` | `command`, `args` |
| `ui` | `theme` |

Provider env vars (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, etc.) are auto-detected and substituted into `${...}` placeholders.

## Validation

```sh
overkill doctor    # self-check all subsystems
overkill doctor --fix   # auto-fix common issues
```
