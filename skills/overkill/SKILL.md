---
name: overkill
description: Configure, extend, or contribute to Overkill — the open-source AI harness with 104+ providers, 10 gateways, and 43 exclusives. Use when the user asks about configuring, setting up, extending, or understanding Overkill itself.
version: 1.0.0
---

# Overkill

Overkill is an open-source terminal AI coding agent written in Go with an Ink+React TUI. 104+ LLM providers, 10 messaging gateways, 43 verified exclusives, a personality engine that learns you, and zero telemetry. Ever.

**Repo:** https://github.com/Sahaj-Tech-ltd/overkill
**Website:** https://overkill.my
**Docs:** https://github.com/Sahaj-Tech-ltd/overkill/wiki

## What makes Overkill different

- **104+ providers** — auto-discovered from models.dev catalog. Never hardcode a model name — use the SmartRouter to pick the cheapest capable model.
- **10 gateways** — Telegram, Discord, Slack, WhatsApp, Signal, Matrix, Email, SMS, HTTP API, Webhooks. Same agent, full tool access, everywhere.
- **43 exclusives** — features no competitor codebase has. Verified against 9 open-source codebases. Not "we do it better" — "nobody else does it at all."
- **Personality engine** — relationship tracking, mood adaptation, situational awareness. Not a blank-slate chatbot.
- **Postgres-backed** — sessions, memory, learning. BadgerDB is dead (2GB vlog corruption). SQLite only for third-party/embedded needs.
- **Zero telemetry** — no analytics, no phoning home, no usage tracking. Ever.

## Quick Start

```bash
# Install
curl -fsSL https://overkill.my/install | sh

# Or with Go
go install github.com/Sahaj-Tech-ltd/overkill@main

# First run auto-bootstraps ~/.overkill/ and launches setup
overkill

# Setup only
overkill setup

# Check config
overkill config
```

## Architecture

```
cmd/overkill/          CLI entrypoint (Cobra)
internal/
  agent/               Core ReAct loop with situational awareness
  providers/           LLM provider factory (104+ auto-discovered)
  personality/         Personality engine + relationship tracking
  security/            Injection detection + pre-exec scanning
  compaction/          LCM dual-state context management
  memory/              Memory orchestration + hot/cold paging
  automation/          SOP engine, routines, cron
  skills/              Skill loader, extractor, safety scanner
  learning/            Evolution engine (self-improvement from corrections)
  seahorse/            Hierarchical DAG summarization
  journal/             Flight recorder + summarizer + alerts
  hotreload/           File watching for config/skills/agents changes
tui/                   Ink+React terminal UI
ui-tui/                New TUI (WIP)
skills/                Bundled skills shipped with binary
```

## Key Files

| File | Role |
|------|------|
| `cmd/overkill/root.go` | Cobra root command, config loading, bootstrap |
| `cmd/overkill/run.go` | `overkill` interactive chat entry point |
| `internal/agent/agent.go` | Core ReAct loop |
| `internal/providers/factory.go` | Provider auto-discovery from models.dev |
| `internal/config/config.go` | Config loading, validation, defaults |
| `internal/skills/loader.go` | Skill loading from `~/.overkill/skills/` |
| `bundled_skills.go` | Go embed of `skills/` dir into binary |

## Config

```yaml
# ~/.overkill/config.toml or config.yaml
[model]
  provider = "deepseek"
  model = "deepseek-v4-pro"

[database]
  url = "postgres://..."  # Postgres required. DATABASE_URL env var fallback.

[gateway]
  enabled = true
  platform = "telegram"
```

Key config rules:
- `database_url` auto-detected from `DATABASE_URL` env var
- Unknown provider types in config are NOT rejected — factory resolves from models.dev catalog
- Config lives in `~/.overkill/config.toml` (TOML, auto-migrating)

## Providers

104+ providers auto-discovered from models.dev. Key ones:

| Provider | Model | Vision |
|----------|-------|--------|
| deepseek | deepseek-v4-pro | No |
| xiaomi | mimo-v2.5-pro | Yes |
| z.ai | glm-5v-turbo | Yes |
| anthropic | claude-sonnet-4 | Yes |
| openai | gpt-4o | Yes |

**CRITICAL:** Never hardcode a model name. Always use the SmartRouter to resolve the cheapest capable model. The only exception: `gpt-4o-mini` is permanently banned — no defaults, no fallbacks, no setup suggestions.

## Skills

Skills are SKILL.md files in `~/.overkill/skills/<name>/`. Format:

```yaml
---
name: skill-name
description: When to use this skill
---
# Skill content
```

Bundled skills (shipped with binary, unpacked on first run):
- `overkill` — this skill
- `debugging` — systematic 4-phase debugging
- `code-review` — comprehensive code review
- `bug-hunt` — systematic bug hunting
- `git-workflow` — Git conventions and workflows
- `testing-pipeline` — test-driven development
- `mutation-test` — mutation testing
- `humanizer` — strip AI-isms from text
- `red-team` — security red-teaming
- `self-modify` — Overkill modifying its own code
- `understand-anything` — codebase comprehension
- `frontend-design` — frontend UI development
- `docx` — Word document manipulation

Skills are loaded from `~/.overkill/skills/` by `internal/skills/loader.go`. The loader scans the directory for subdirectories containing `SKILL.md` files.

## Coding conventions

- **Go:** gofmt, golangci-lint, errors wrapped with context. No panic in library code.
- **Tests:** Use standard library `testing`. Table-driven tests preferred.
- **Errors:** Best-effort patterns — failures never block the agent loop.
- **Interfaces:** Defined at consumer, not implementation.
- **Journal:** Fail-open — broken journal never blocks sessions.
- **Postgres:** All persistent stores use Postgres. SQLite only for third-party needs.

## CLI reference

```bash
overkill                    Interactive chat
overkill chat -q "..."     Single query
overkill setup              Interactive setup wizard
overkill config             View/edit config
overkill config set K V     Set a config value
overkill skills list        List installed skills
overkill skills install URL Install a skill
overkill doctor             Check dependencies
overkill gateway run        Start messaging gateway
overkill update             Check for updates
```

## Gateway platforms

Telegram, Discord, Slack, WhatsApp, Signal, Matrix, Email, SMS, HTTP API, Webhooks.

## Exclusives (key ones)

- **Situational awareness** — pre-action reflection: content type, user model (ADHD/mobile), tool inventory, modality choice
- **Personality engine** — relationship tracking + mood adaptation + fun facts
- **SmartRouter** — picks cheapest capable model per task from 104+ providers
- **Seahorse** — hierarchical DAG summarization of complex outputs
- **Evolution engine** — self-improvement from user corrections
- **Bubblewrap** — per-tool isolation sandboxing
- **Flight recorder** — append-only journal with pattern detection

Full list: https://overkill.my/exclusives

## Communication style

- Direct and concise. No "great question!" preambles.
- Match the user's energy. Terse when rushing, casual when not.
- Admit mistakes quickly.
- Call out bad ideas directly — constructively.
- Personality is a feature — humor, fun facts, mirroring.

## Key rules for coding agents working on Overkill

1. **Never push to GitHub without explicit approval.**
2. **Never hardcode a model name** — use SmartRouter.
3. **gpt-4o-mini is banned.**
4. **Postgres for persistence.** BadgerDB is dead.
5. **Best-effort everywhere.** Failures don't block the agent loop.
6. **Plan before building.** Check the master plan at `~/.overkill/plans/overkill-master-plan.md`.
7. **Internal docs stay off GitHub.** `.gitignore` must list specific files, not `*.md`.
8. **Build with `go build ./cmd/overkill`.** Don't use `go run` for production.
