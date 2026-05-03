---
name: understand-anything
version: 1.0.0
description: Ramp onto an unfamiliar codebase, document, or system fast. Builds a layered mental model — overview, modules, data flow, gotchas — by combining structural scan with targeted deep reads. Use on first contact with any unknown repo or large artifact.
author: ethos-team
category: introspection
tags: [onboarding, exploration, codebase, documentation]
triggers: [understand, explore, "ramp on", "explain this codebase", "what does this do", onboard]
enabled: true
---

# Understand Anything

A repeatable protocol for going from "never seen this" to "can navigate it confidently" on any codebase, document, or system.

## When to use

- First contact with a new repo
- Auditing a third-party library before integrating
- Inheriting code from a teammate or contractor
- Reverse-engineering an API or binary
- Reading a long PDF / specification / RFC

## Phase 1 — Scan (depth = 0)

Spend 5 minutes building a one-page map. Resist the urge to dive into files.

For a codebase:

1. **Read README, CONTRIBUTING, AGENTS.md, CLAUDE.md** — author intent.
2. **List top-level directories** — package.json/go.mod/Cargo.toml/pyproject.toml gives you stack.
3. **Count files per directory** — big dirs = important dirs.
4. **Identify entry points** — `main.go`, `cli.ts`, `__main__.py`, `index.js`, `cmd/`.
5. **Identify config files** — what's tunable.

For a document:

1. Read the table of contents and the abstract / executive summary.
2. Skim figures, tables, and section headings.
3. Note the author's vocabulary (terms that recur 5+ times).

Output of Phase 1: a 5-bullet "what this is" plus a list of the 3–5 most important paths or sections.

## Phase 2 — Trace (depth = 1)

Pick **one user-visible flow** and trace it end to end.

For a codebase:

- "How does a request enter and where does the response come from?"
- For a CLI: "What happens when I run `<binary> <subcommand>`?"
- For a library: "What does the most prominent example in the README actually do?"

Walk the call graph. Note:

- Where data is validated
- Where errors are caught
- Where state lives (DB, cache, in-memory, file)
- Where the boundaries are (network, disk, third-party)

One trace beats reading ten random files.

## Phase 3 — Map (depth = 2)

Now produce a written artifact you'd hand to the next person ramping on this:

```markdown
# <System> overview

## What it is
<two sentences>

## Core concepts
- <concept>: <one-line definition>
- <concept>: <one-line definition>

## Architecture
<text or ASCII diagram showing the major components and arrows>

## Key files
- `path/a.go` — <what lives here>
- `path/b.go` — <what lives here>

## Conventions
- <how errors are handled>
- <how config is loaded>
- <how tests are structured>

## Gotchas
- <thing that surprised you>
- <thing that contradicts the README>

## Open questions
- <what you couldn't figure out>
```

Save it. The next time you (or a teammate) returns, this saves an hour.

## Phase 4 — Stress (depth = 3)

Optional. Use when stakes are high (security review, taking ownership, vendor evaluation):

- Run the test suite. What fails? What's flaky?
- Read the issue tracker for "wontfix" and "known issue" labels.
- Diff against a recent commit on `main` to see what's actively changing.
- Try a deliberate mistake (bad input, missing config) and observe behavior.

## Anti-patterns

- **Reading top to bottom.** Doesn't scale beyond ~2k lines.
- **Tab-fishing.** Opening 30 files hoping understanding emerges. It won't.
- **Over-trusting docs.** Code is truth; docs are wishes.
- **Skipping tests.** Tests document the contract better than prose.

## Output format

Return the Phase 3 map as a markdown document. If the user wants to go deeper, run Phase 4 and append a "Stress findings" section.
