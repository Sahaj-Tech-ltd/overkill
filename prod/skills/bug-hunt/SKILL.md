---
name: systematic-bug-hunt
description: Use when auditing a codebase for bugs, running a full codebase sweep, or the user says "find bugs", "audit this", "sweep", or "what's broken". Also use before any release. Load BEFORE spawning sub-agent audit sweeps — this is the checklist they must follow.
---

# Systematic Bug Hunt

## The Core Lesson

**LLMs find what you ask for.** A narrow prompt ("find nil pointers and race conditions") will miss config bugs, security flaws, design issues, and compile blockers. The checklist IS the skill. The model is just the execution engine.

## The 8-Category Checklist

For EVERY file, check ALL of the following:

### 1. RUNTIME BUGS
- Nil pointer dereferences, index out of bounds, close of closed channel
- Unhandled errors (`_ = err`, result assigned to `_`)
- Goroutine leaks (goroutines with no exit path, no ctx.Done())
- Data races (shared mutable state without locks)
- Deadlocks (lock ordering, recursive RLock, channel blocks)

### 2. HARDCODED VALUES
- Model names baked into source (e.g., `"gpt-4o-mini"`, `"gpt-4o"`)
- API endpoints, ports, timeouts baked as literals
- Credentials or secrets in source (even as defaults)
- Paths specific to one developer's machine (`/home/user/...`)

### 3. PERSISTENCE BUGS
- Values written to disk without validation
- Non-atomic file writes (truncate-then-write without temp+rename+fsync)
- Missing fsync before rename
- `ON CONFLICT` clauses that silently discard fields
- Migrations that run on startup and permanently overwrite user config

### 4. DESIGN FLAWS
- Check-then-act races (TOCTOU)
- Functions defined but never called
- Interfaces implemented but no code path reaches the implementation
- Schema/config fields with no runtime consumer
- Capacity limits that can be bypassed

### 5. SECURITY
- Path traversal: user input in `filepath.Join` without containment check
- Command injection: user input in `exec.Command` or `sh -c`
- API keys in URLs, query params, logged headers, or serialized struct fields
- Timing side-channels on secret comparisons (`==` instead of `subtle.ConstantTimeCompare`)
- Auth bypasses (checks on wrong variable, check-vs-use mismatch)
- Parent environment inherited by child processes

### 6. SILENT FAILURE
- Errors swallowed with `_` and no log
- `.catch(() => {})` with zero feedback
- Fallback values that silently change behavior for most users
- Stub/no-op handlers that look functional (`void x; void y;`)

### 7. COMPILE BLOCKERS
- Functions declared with no body
- Methods called that are never defined (check cross-package)
- Type assertions accessing concrete fields on interfaces
- File truncation (struct/function cut off mid-definition)

### 8. DATA INTEGRITY
- Dedup logic that silently drops fields on conflict
- Merge operations without transactions
- TOCTOU between load and save
- Fields with no database column that appear to persist

## Batching Strategy

**Group by domain, NOT alphabetically.** The model needs to trace cross-package calls:

| Batch | Packages |
|-------|----------|
| 1 | Config + auth + secrets + hierarchy + profiles |
| 2 | All providers together |
| 3 | Agent core + subsystems (goal, checkpoint, skills, content_classify) |
| 4 | Memory + compaction + speculative + automemory + tokenizer |
| 5 | Session + sync + db + checkpoint + atomicfile |
| 6 | All gateway bots + dispatch + hub + router |
| 7 | Personality + learning + skills + settings |
| 8 | Journal + drift + credit + walls + audit + events |
| 9 | Subagent + LATS + pipeline + worktree + web + API + LSP + browser |
| 10 | cmd/overkill/ (all adapters, bootstrap, run, daemon, serve) |
| 11 | TUI src/ — app, boot, memo, chat, sidebar, dialogs, panels |
| 12 | TUI src/ — remaining components |

**Run batches in parallel**, then consolidate.

## Consolidation

After all batches complete:
1. Read all outputs
2. **Deduplicate by symptom, not location** — same bug described two ways = one bug
3. Assign severity: COMPILE BLOCKER > CRITICAL > HIGH > MEDIUM > LOW
4. Group into domains (Config, Gateway, Memory, etc.)
5. Write the report to `bugs.md`

## Severity Definitions

| Severity | Definition |
|----------|------------|
| **COMPILE BLOCKER** | Prevents `go build` or `npx tsc --noEmit` |
| **CRITICAL** | Data loss, RCE, path traversal, silent corruption, nil panic on common path |
| **HIGH** | Broken feature, security bypass, goroutine leak, hardcoded wrong value |
| **MEDIUM** | Silent error swallowing, race condition on slow path, bad heuristic |
| **LOW** | Style, dead code, unused variable, cosmetic |

## Anti-Patterns

- **Don't split related packages** across batches — config+auth together, all providers together
- **Don't use narrow prompts** — always use all 8 categories
- **Don't skip consolidation** — raw batch outputs are noisy and duplicative
- **Don't trust initial severity** — re-evaluate during consolidation
