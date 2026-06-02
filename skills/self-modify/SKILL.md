---
name: overkill-self-modify
description: Use when the agent needs to modify Overkill itself — config, skills, code, providers, or any part of its own runtime. Also use when debugging Overkill from the inside, adding features to itself, or fixing its own bugs.
---

# Overkill Self-Modification

## Overview

Overkill can and should modify itself. Config, skills, provider settings, even Go/TypeScript code — all fair game. An agent that can't improve its own home is half an agent.

## Repository & Config Layout

| What | Where |
|---|---|
| Go source | `~/docker/overkill/` |
| TUI (Ink/React) | `~/docker/overkill/tui/` |
| Skills | `~/docker/overkill/skills/` |
| Config | `~/.overkill/config.toml` |
| User overrides | `~/.overkill/user.yaml` |
| Plans (HTML) | `~/.overkill/plans/` |

## Modifying Config

Read: `cat ~/.overkill/config.toml`
Edit: `patch` tool or `sed` on the file
The running agent picks up changes via hotreload (config file watcher).

Key sections:
- `[agent]` — name, provider, model, max_turns, system_prompt
- `[thinking]` — level (off/minimal/low/medium/high/x-high)
- `[[providers]]` — LLM provider blocks (name, type, api_key, base_url, models)
- `[personality]`, `[security]`, `[cost]`, `[compaction]`

## Modifying Skills

Skills are SKILL.md files at `~/docker/overkill/skills/<name>/SKILL.md`.

To create a new skill: use `skill_manage(action='create', ...)`
To edit: use `skill_manage(action='patch', ...)` or `skill_manage(action='edit', ...)`

Skills are loaded by the agent at startup. New skills take effect next session.

## Modifying Go Code

```bash
cd ~/docker/overkill
# Edit files
go build ./...        # verify
go test ./...         # verify
```

Key packages:
- `internal/agent/` — core ReAct loop
- `internal/config/` — TOML config loading, validation, migration
- `internal/providers/` — LLM adapters
- `internal/tools/` — built-in tools
- `internal/api/` — TUI API server
- `internal/gateway/` — Slack/Telegram/Discord dispatch
- `cmd/overkill/` — CLI entrypoint

## Modifying TUI (TypeScript/Ink)

```bash
cd ~/docker/overkill/tui
# Edit files in src/
npx tsc --noEmit    # typecheck
```

Key files:
- `src/app.tsx` — main app, keybindings, mode state
- `src/components/settings/` — settings panel tabs
- `src/components/chat/` — chat view
- `src/hooks/` — React hooks (useChat, useBackend, etc.)

## Safety Rules

1. **Back up before changing config**: `cp ~/.overkill/config.toml ~/.overkill/config.toml.bak`
2. **Never push to GitHub** without explicit user approval
3. **Don't delete provider API keys** — comment them out if needed
4. **Always `go build` after Go changes** — broken builds brick the agent
5. **Always `npx tsc --noEmit` after TS changes**
6. **Restart the TUI** (`overkill` in a new terminal) after code changes

## Self-Improvement Loop

When asked to improve itself:
1. Identify what needs changing (config, skill, code)
2. Make the change
3. Verify (build, typecheck, test)
4. Tell the user what changed and how to activate it

## Common Self-Mod Tasks

**Add a provider:**
```bash
cd ~/docker/overkill
# Edit ~/.overkill/config.toml — add [[providers]] block
# Or: overkill config set provider.<name>.api_key sk-...
```

**Add a tool:**
1. Create `internal/tools/<name>.go`
2. Register in `cmd/overkill/run.go` or `internal/api/server.go`
3. `go build ./...`

**Change system prompt:**
Edit `~/.overkill/config.toml` → `[agent]` → `system_prompt = "..."` 
Or use the TUI: Settings → System tab → Edit

**Fix a bug in the agent loop:**
1. Edit `internal/agent/agent.go`
2. `go test ./internal/agent/...`
3. `go build ./...`
