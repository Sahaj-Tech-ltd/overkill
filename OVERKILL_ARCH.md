# Architecture — overkill
_generated 2026-05-26 — extend as the project evolves_

## Reading this file

This is the **canonical architecture reference** for the project (master plan §6.5 Wall 2). Wall 2 reads this file before non-trivial changes to flag drift. Skills that touch architecture write here. Two principles inherited from the plan:

1. **Deletion test** — when uncertain whether a module is earning its keep, imagine deleting it. If the complexity vanishes, it was a pass-through. If it reappears across N callers, it was earning its keep.
2. **One adapter = hypothetical seam, two adapters = real seam.** Don't engineer interfaces for single implementations.

## Package manifests

- **`.opencode`** (node) — /home/harsh/docker/overkill/.opencode/package.json
- **`bridge`** (python) — /home/harsh/docker/overkill/bridge/pyproject.toml
- **`github.com/Sahaj-Tech-ltd/overkill`** (go) — /home/harsh/docker/overkill/go.mod

## Top-level layout

```
.git/
.github/
.opencode/
AGENTS.md
Brewfile
CHANGELOG.md
CONTRIBUTING.md
Dockerfile
LICENSE-APACHE
LICENSE-MIT
Makefile
README.md
ROADMAP.md
SECURITY.md
WALLS.md
Wants and a to do list.html
bin/
bridge/
bugs.md
cmd/
coverage.out
docker-compose.yml
docs/
examples/
go.mod
go.sum
inspiration/
install.sh
install_test.sh
internal/
models/
optional-skills/
out-of-scope.md
overkill
overkill-master-plan.md
phase5-plan.md
pkg/
plugins/
research/
scripts/
skills/
tests/
```

## Layers

_Fill in once boundaries stabilise. Typical structure:_

- **Edge** — CLI / TUI / gateways. User-facing surface.
- **Orchestration** — agent loop, sub-agents, hooks.
- **Domain** — providers, memory, skills, walls. The product logic.
- **Infrastructure** — BadgerDB, bridge, journal. Persistence + IPC.

## Invariants

_Things that MUST hold across changes. Document each with a one-line test._

- Example: "The agent's history is owned by the Agent type — sub-agents take a snapshot, never a reference."

## ADRs

_Architectural Decision Records live in `docs/adr/`. Per §6.5, only write an ADR when ALL THREE conditions hold:_

1. The decision is **hard to reverse**.
2. The decision is **surprising without context**.
3. The decision is the result of a **real trade-off** (not the obvious choice).

## Performance smells

_Catalog of architectural anti-patterns observed in this codebase, with the canonical fix._

- Example: "Sync endpoint added to async layer — surface in next-turn warning."
