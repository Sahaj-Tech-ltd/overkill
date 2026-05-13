<div align="center">

```
  ██████╗ ████████╗██╗  ██╗ ██████╗ ███████╗
 ██╔════╝ ╚══██╔══╝██║  ██║██╔═══██╗██╔════╝
 ██║         ██║   ███████║██║   ██║███████╗
 ██║         ██║   ██╔══██║██║   ██║╚════██║
 ╚██████╗    ██║   ██║  ██║╚██████╔╝███████║
  ╚═════╝    ╚═╝   ╚═╝  ╚═╝ ╚═════╝ ╚══════╝
```

# overkill

### the vibe-coding agent that actually has discipline.

[![Go](https://img.shields.io/badge/go-1.23+-00ADD8?style=flat-square&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT%20%7C%20Apache--2.0-blue?style=flat-square)](#license)
[![Build](https://img.shields.io/badge/build-passing-success?style=flat-square)](#)
[![Release](https://img.shields.io/badge/release-pre--alpha-yellow?style=flat-square)](#)

</div>

Overkill is a terminal coding agent that lives in your shell. It streams tool calls,
gates risky actions behind explicit approval, manages its own context with a
compact/fork model, and keeps every session local in an embedded BadgerDB store.
It speaks to a dozen LLM providers, the Agent Communication Protocol, MCP
servers, and the language servers you already have installed.

It is opinionated about the agent loop, the surface area of its tools, and what
it asks you for permission to do. Everything else — themes, keybindings,
plugins, providers, sync backends — is yours to configure.

---

## Install

### One-liner

```sh
curl -fsSL https://raw.githubusercontent.com/Sahaj-Tech-ltd/overkill/main/install.sh | sh
```

The installer detects your platform, prefers `go install` when Go is on `PATH`,
otherwise downloads a pre-built binary, drops it in `~/.local/bin/overkill`, and
bootstraps `~/.overkill/`. See [`install.sh`](install.sh) for everything it does.

### With Go

```sh
go install github.com/Sahaj-Tech-ltd/overkill/cmd/overkill@latest
```

### From source

```sh
git clone https://github.com/Sahaj-Tech-ltd/overkill.git
cd overkill
make install-all   # builds and copies to ~/go/bin/overkill
```

### Runtime dependencies

Only `git` is required at runtime. Optional, auto-detected:
`gopls`, `typescript-language-server`, `pyright`, `rust-analyzer` for LSP tools;
any MCP server you wire up; `gh` if you want GitHub Gist sharing.

---

## Quick start

```sh
overkill                        # launch the TUI
overkill doctor                 # self-test every subsystem with fix hints
overkill --help                 # CLI subcommands
```

First launch runs an onboarding wizard: pick a provider, paste an API key (or
do an OAuth device flow for Anthropic/Copilot), pick a model, pick a theme.
Then type a question and hit Enter. Type `/help` for the full surface.

<!-- SCREENSHOT -->
<!-- Screenshots intentionally omitted for the pre-alpha drop. They will land
     once the boot animation, the cell renderer, and the diff viewer have
     stabilised on a single demo workflow. -->

---

## Features

Everything below ships in the current tree. Nothing aspirational.

### Agent loop
- ReAct loop with streaming, tool calls, approval gating
- `/compact` to summarise history, `/fork` to branch from any past message
- Variants: run the same prompt across N models, side-by-side
- Confidence scoring per response, recovery on tool failure
- Forethought pass before action; spec-driver mode for plan-first work

### TUI
- Layered overlay system inspired by opentui
- Two themes shipped: Catppuccin Mocha and Tokyo Night Storm
- 20+ dialogs: commands, models, sessions, themes, config, MCP, plugins,
  worktrees, permissions, tags, variants, workspaces, subagents, skills,
  diff viewer, file viewer, stash, share, sync, ACP, status, fork picker
- Slash command palette with autocomplete
- File mention picker (`@path`), prompt history, prompt stash
- Sectioned `/help`, sticky onboarding wizard

### Animations
- Logo shimmer, background pulse during generation, toast slide, boot fade
- All gated behind `OVERKILL_NO_ANIMATIONS=1` for SSH and slow links

### Cell-level renderer
- Opt-in via `OVERKILL_CELL_RENDER=1`
- Roughly 215× byte reduction over SSH for typical incremental frames
  vs naive full-screen redraw

### Web UI
- `overkill web --open` — serves a single-page browser UI on
  `127.0.0.1:8420` and opens it in the default browser.
- Same agent backend as the TUI, same BadgerDB session store.
- LAN access: `overkill web --listen 0.0.0.0:8420`. Always keep the bearer
  token (`~/.overkill/web-token`) private — anyone with the token can drive
  the agent. The startup banner prints a URL with the token embedded as a
  one-time `?t=…` query that the page promotes to a cookie.
- `--no-auth` is allowed but only for localhost binds.
- Mobile-first responsive layout; works down to 360 px wide. Future:
  emit a QR code for the URL so phones can join without typing the token.

### Providers
- First-class adapters: OpenAI, Anthropic, Google Gemini, DeepSeek, Ollama,
  OpenRouter
- Generic OpenAI-compatible adapter covers Groq, xAI, Mistral, Together,
  Perplexity, and any other compat endpoint via the `custom` provider type
- Live model catalog from `models.dev` with 24h disk cache
- OAuth device-code flow for Anthropic and GitHub Copilot

### Tools
`shell`, `fs` (read/write/edit), `git`, `git_preview`, `grep`, `web`,
`patch` (with side-by-side diff render), `pty_shell`, `worktree`
(list/add/remove), `tags` (add/remove/list), `ask_user`, `acp_send`,
LSP tools (`lsp_definition`, `lsp_references`, `lsp_hover`, `lsp_symbols`),
`delegate` to a sub-agent.

### Sessions
- Per-folder BadgerDB store under `~/.overkill/sessions/`
- list / switch / rename / delete / new
- Autosave each turn, fork from any past message

### Sync
- Push/pull session state to S3-compatible storage, a git remote, or a shared
  filesystem path. Background autopush available.

### Share
- Render any session as a self-contained HTML page
- Upload to GitHub Gist or `transfer.sh`
- Link copied to your clipboard via OSC 52 (works through SSH)

### MCP
- Connect any MCP server (`stdio` or HTTP). Tools auto-register into the
  agent's tool registry on boot.

### LSP
- Auto-detects gopls, typescript-language-server, pyright, rust-analyzer
- Exposes definitions, references, hover, and symbol search to the agent

### ACP (Agent Communication Protocol)
- HTTP + SSE server other agents can dispatch tasks to
- Bearer-token auth; `overkill acp serve` / `acp token` / `acp ping`

### Plugins
- Subprocess JSON-RPC plugin runtime
- Plugins extend tools, slash commands, lifecycle events, and context providers
- Go SDK at `examples/plugins/sdk-go/`
- Two reference plugins shipped: `examples/plugins/notes/` and
  `examples/plugins/git-stats/`
- Install with `overkill plugin install <git-url>`

### Sub-agents
- Goroutine-based child spawner with file-state tracking and cost rollup
- Use the `delegate` tool from inside any session

### Personality
- Relationship tracker, style inference, blindspot detection
- Model fingerprint, cold-start protocol
- `~/.overkill/memories/soul.md` is yours to edit

### Permissions
- Tool calls are risk-classified
- Per-call dialog: allow once / allow session / deny
- Append-only ledger at `~/.overkill/permissions.log`

### Workspaces
- Switch between projects from inside the TUI; sessions follow the workspace

### Skills
- Installable skill packs in `~/.overkill/skills/` (SKILL.md format)
- Bundled skills under `skills/` in the repo

### Walls
- Three quality gates: architecture, ouroboros (loop detection), test-quality
- Each can be disabled via config

### Doctor
- `overkill doctor` self-tests config, providers, MCP, LSP, plugins, sync,
  cell renderer, and animations — with concrete fix hints per failure

---

## Configuration

Config lives at `~/.overkill/config.toml`. Example:

```toml
[agent]
default_model = "anthropic/claude-sonnet-4-5"
max_tokens    = 8192
temperature   = 0.2

[providers.openai]
type    = "openai"
api_key = "${OPENAI_API_KEY}"

[providers.anthropic]
type    = "anthropic"
api_key = "${ANTHROPIC_API_KEY}"

[providers.ollama]
type     = "ollama"
base_url = "http://localhost:11434"

[sync]
backend = "s3"          # one of: s3 | git | file
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

Provider env vars (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, …)
are auto-detected and substituted into `${...}` placeholders.

---

## Slash commands

| Command | Description |
|---|---|
| `/help` | show keybinding help |
| `/clear` | clear chat history |
| `/quit` | exit overkill |
| `/model` | open model picker |
| `/sessions` | switch session |
| `/theme` | open theme picker |
| `/config` | reconfigure provider |
| `/compact` | compact chat history |
| `/init` | write a starter `.overkill/` config |
| `/status` | show provider, model, session status |
| `/fork` | fork the conversation from a past message |
| `/stash` | stash the current draft (or `list` to browse) |
| `/diff <path>` | show a unified diff (append `s` for side-by-side) |
| `/mcp` | show MCP server status & tools |
| `/plugins` | show installed plugins & status |
| `/worktree` | manage git worktrees |
| `/permissions` | view permission ledger |
| `/tags` | browse file tags |
| `/variant` | compare a prompt across models |
| `/workspace` | switch between projects |
| `/subagents` | open subagent detail view |
| `/skills` | manage installed skills |
| `/view <path>` | open a file in the split-view pane |
| `/sync` | push/pull session sync to remote backend |
| `/share` | share current session as a public URL |
| `/acp` | show ACP server status & token |

---

## Keybindings

| Key | Action |
|---|---|
| `ctrl+k` | command palette |
| `ctrl+o` | model picker |
| `ctrl+s` | session switcher |
| `ctrl+t` | theme picker |
| `ctrl+,` / `F2` | config |
| `ctrl+i` | status |
| `ctrl+f` | fork from message |
| `ctrl+h` | help overlay |
| `esc` | close dialog / cancel |
| `ctrl+c` | quit |

---

## Environment variables

| Variable | Effect |
|---|---|
| `OVERKILL_CELL_RENDER=1` | enable the cell-level renderer (SSH-friendly) |
| `OVERKILL_NO_ANIMATIONS=1` | disable all animations |
| `OVERKILL_PROMPT_DEBUG=1` | log prompt mount/unmount events |
| `OPENAI_API_KEY` etc. | provider credentials, auto-detected |
| `EDITOR` | external editor for the prompt |

---

## Plugins

Plugins are subprocesses overkill spawns and talks to over JSON-RPC 2.0 on stdio.
They can register tools, slash commands, lifecycle event handlers, and context
providers. They are discovered from `~/.overkill/plugins/<name>/` (each directory
must contain an executable named after the directory).

Reference plugins:
- [`examples/plugins/notes/`](examples/plugins/notes/) — adds a `notes` tool
  backed by a local file
- [`examples/plugins/git-stats/`](examples/plugins/git-stats/) — adds a
  `git_stats` tool that reports churn

Minimal plugin using the Go SDK at [`examples/plugins/sdk-go/`](examples/plugins/sdk-go/):

```go
p := sdk.New(sdk.Manifest{Name: "hello", Version: "0.1.0"})
p.OnTool("greet", func(ctx context.Context, args json.RawMessage) (any, error) {
    return map[string]string{"text": "hi!"}, nil
})
p.RegisterTool(sdk.ToolDecl{Name: "greet", Description: "say hi"})
p.Run()
```

Install:

```sh
overkill plugin install https://github.com/you/your-overkill-plugin
```

---

## Architecture

The agent loop lives in `internal/agent/` (ReAct, streaming, variants,
forethought, recovery). Storage is BadgerDB under `~/.overkill/sessions/` keyed
by working directory. Provider adapters in `internal/providers/` are gated
behind a single `Provider` interface; the catalog is hydrated from
`models.dev` and cached for 24 hours.

The per-package wiring map is at [`docs/AUDIT-2026-05-03.md`](docs/AUDIT-2026-05-03.md).

---

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). For bugs and features, open an issue
before sending a PR — the issue templates ask the right questions.

---

## License

Dual-licensed under MIT or Apache-2.0 at your option. See
[`LICENSE-MIT`](LICENSE-MIT) and [`LICENSE-APACHE`](LICENSE-APACHE).

---

## Acknowledgements

Overkill stands on the shoulders of projects that did the hard thinking first.

- [opencode](https://github.com/sst/opencode) — TUI patterns and provider factory shape
- [opentui](https://github.com/SBoudrias/opentui) — overlay/layer renderer model
- [models.dev](https://models.dev) — live model pricing and capability catalog
- [Hermes](https://github.com/hermes-agent) — agent loop and journaling shape
- [picoclaw](https://github.com/picoclaw) — minimalist CLI ergonomics
- [openclaw](https://github.com/openclaw) — repo template patterns and quality gates
- [claude-mem](https://github.com/claude-mem) — memory and personality concepts

Each shaped a specific subsystem; none are vendored.
