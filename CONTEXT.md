# Glossary — overkill
_generated 2026-05-26 — the canonical vocabulary for this project. All skills read from it; all skills write to it when terms are established. When you find yourself explaining a term mid-conversation, add it here._

## How to use this file

- **One term per entry.** Lowercase, hyphenated identifier.
- **One-line definition.** Plain English. Avoid circular references.
- **Example** (optional). A canonical use that pins the meaning.

## Terms

_The list grows as the project develops. Initial entries are placeholders — replace or extend._

### `tracer-bullet-issue`

A thin vertical slice that cuts through ALL architectural layers (schema → API → UI → tests) and is independently demoable. Contrasted with horizontal slices (one issue per layer).

_Example: "add `/auth/whoami` endpoint" — touches handler, route registration, integration test, and (if surfaced) a UI element. One ticket._

### `HITL` / `AFK`

Issue classification. HITL (Human-In-The-Loop) requires human review before merge. AFK (Away-From-Keyboard) the agent can implement and merge independently.

### `deletion-test`

The check used to decide whether a module is earning its keep: imagine deleting it. If complexity vanishes, it was a pass-through. If complexity reappears across N callers, the module was real abstraction.

