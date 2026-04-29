<div align="center">

```
  ██████╗ ████████╗██╗   ██╗ ██████╗ ███████╗
 ██╔════╝ ╚══██╔══╝██║   ██║██╔═══██╗██╔════╝
 ██║         ██║   ███████║██║   ██║███████╗
 ██║         ██║   ██╔══██║██║   ██║╚════██║
 ╚██████╗    ██║   ██║   ██║╚██████╔╝███████║
  ╚═════╝    ╚═╝   ╚═╝   ╚═╝ ╚═════╝ ╚══════╝
```

### Your AI coding agent with personality.

[![Go Reference](https://img.shields.io/badge/go-1.23-00ADD8?style=flat-square&logo=go)](https://go.dev/)
[![Python](https://img.shields.io/badge/python-3.12-3776AB?style=flat-square&logo=python)](https://python.org/)
[![License](https://img.shields.io/badge/license-MIT%20%7C%20Apache--2.0-blue?style=flat-square)](LICENSE)
[![Build](https://img.shields.io/github/actions/workflow/status/Sahaj-Tech-ltd/ethos/tests.yml?style=flat-square&label=tests)](https://github.com/Sahaj-Tech-ltd/ethos/actions)
[![Stars](https://img.shields.io/github/stars/Sahaj-Tech-ltd/ethos?style=flat-square)](https://github.com/Sahaj-Tech-ltd/ethos)

[Docs](#configuration) · [Install](#install) · [Contributing](CONTRIBUTING.md)

</div>

---

Ethos is an open-source AI coding agent that lives in your terminal. It reasons before acting, manages its own context window, has actual personality, and produces quality code through an incremental pipeline.

**What makes Ethos different:**

- 🧠 **ReAct agent loop** — thinks, acts, observes. No blind execution.
- 🔒 **Security-first** — prompt injection detection, command scanning, path blocking
- 🗜️ **LCM-inspired compaction** — context management that actually works
- 🎭 **Personality engine** — your agent has character. Subtle, witty, or off. Your call.
- 📓 **Flight recorder** — every session logged, journaled, traceable
- 🔀 **Smart routing** — routes tasks to the right model based on complexity
- 🏠 **Fully local** — BadgerDB storage, no cloud dependency, your data stays yours
- 🌉 **Go + Python bridge** — Go for speed, Python for ML (embeddings, reranking, vectors)

## Install

```bash
# With Go
go install github.com/Sahaj-Tech-ltd/ethos/cmd/ethos@latest

# Or with Docker
docker pull ghcr.io/sahaj-tech-ltd/ethos:latest

# Or build from source
git clone https://github.com/Sahaj-Tech-ltd/ethos.git
cd ethos && make install
```

## Quick Start

```bash
# First run — creates ~/.ethos/ with defaults
ethos

# In a project directory
cd my-project
ethos

# Ask it anything
ethos "refactor the auth module to use JWT"
```

## Architecture

```
┌─────────────────────────────────────────────────┐
│                    TUI (Bubble Tea)              │
├─────────────────────────────────────────────────┤
│  Agent Loop (ReAct: Think → Act → Observe)      │
│  ┌──────────┐ ┌──────────┐ ┌──────────────────┐ │
│  │Security  │ │Personality│ │Prompt Rewriter   │ │
│  │Plane     │ │Engine    │ │(Anti-bloat)      │ │
│  └──────────┘ └──────────┘ └──────────────────┘ │
│  ┌──────────┐ ┌──────────┐ ┌──────────────────┐ │
│  │Compaction│ │Routing   │ │Session Manager   │ │
│  │(LCM)     │ │(Complexity)│ │(BadgerDB)       │ │
│  └──────────┘ └──────────┘ └──────────────────┘ │
├─────────────────────────────────────────────────┤
│  Tools: Shell │ FS │ Git │ Grep │ Web │ Skills  │
├─────────────────────────────────────────────────┤
│  Providers: OpenAI │ Anthropic │ Gemini │ Ollama │
├─────────────────────────────────────────────────┤
│  Python Bridge (gRPC)                           │
│  Embeddings │ Reranking │ Vector Memory │ LLM   │
└─────────────────────────────────────────────────┘
```

## Comparison

| Feature | Ethos | Claude Code | OpenCode | Aider |
|---------|-------|-------------|----------|-------|
| Language | Go + Python | TypeScript | Go | Python |
| Agent Loop | ReAct + Forethought | ReAct | ReAct | Simple |
| Context Compaction | LCM-inspired | Native | None | Repo map |
| Personality | Configurable | None | None | None |
| Security Plane | Injection detection + command scanning | Basic | Basic | None |
| Local Storage | BadgerDB | JSON files | BadgerDB | Git |
| Model Routing | Complexity-based | Fixed | Fixed | Manual |
| Journal/Diary | Flight recorder + sub-agent | None | None | None |
| Memory | Vector (BadgerDB/Qdrant) | None | None | None |
| Provider Support | 8+ providers | Anthropic only | 6+ providers | 6+ providers |
| Python Bridge | gRPC | N/A | N/A | N/A |
| License | MIT + Apache-2.0 | Proprietary | MIT | Apache-2.0 |

## Configuration

Ethos uses TOML config at `~/.ethos/config.toml`:

```toml
[agent]
name = "Butter"           # Your agent's name
personality = "subtle"    # subtle | witty | full | off
autonomy = "supervised"   # readonly | supervised | full

[model]
default = "anthropic/claude-sonnet-4"
cheap = "anthropic/claude-haiku-3.5"
routing = true            # Auto-route by complexity

[security]
injection_detection = true
command_scanning = true
forbidden_paths = ["/etc", "~/.ssh"]
```

## Runtime Directory

```
~/.ethos/
├── config.toml       # Your config
├── memories/         # soul.md, user.md, relationship state
├── plans/            # Session plans
├── journal/          # Raw logs + diary summaries
├── sessions/         # Session data (BadgerDB)
├── skills/           # Your custom skills
└── work/             # Working directory
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full guide. Quick version:

```bash
go build ./...
go test ./...
golangci-lint run
```

One concern per PR. Conventional Commits. AI-assisted PRs must disclose.

## License

Dual-licensed under [MIT](LICENSE-MIT) and [Apache-2.0](LICENSE-APACHE). Pick whichever works for you.

---

## Star History

<a href="https://www.star-history.com/?repos=Sahaj-Tech-ltd%2Fethos&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=Sahaj-Tech-ltd/ethos&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=Sahaj-Tech-ltd/ethos&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=Sahaj-Tech-ltd/ethos&type=date&legend=top-left" />
 </picture>
</a>

<div align="center">

### Contributors

<!-- AUTO-UPDATED by contributors.yml workflow -->

</div>
