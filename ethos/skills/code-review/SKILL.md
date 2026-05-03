---
name: code-review
version: 1.0.0
description: Disciplined code review pass focused on correctness, security, and maintainability. Use after writing or modifying code, before committing, when reviewing PRs, or when the user asks to "review", "critique", or "check" code.
author: ethos-team
category: review
tags: [code, review, quality, security]
triggers: [review, "code review", critique, audit, "check this"]
enabled: true
---

# Code Review

A focused review pass for changed code. Skip phases only when explicitly justified.

## When to use

- Immediately after writing or modifying code
- Before committing to a shared branch
- When asked to review a PR or diff
- When user reports the code "feels off" without specifics

## Phase 1 — Scope the diff

Before reading line-by-line:

- [ ] Identify the changed files and the public surface area touched
- [ ] Note new dependencies introduced
- [ ] Note any deletions (especially tests, error handling, validation)

A 500-line diff with one critical regression hidden in line 73 is the norm. Skim for shape first; depth-read second.

## Phase 2 — Correctness

For every changed function:

1. **Edge cases.** Empty inputs, nil/null, zero, negative, overflow, unicode, very long.
2. **Error paths.** Every `err != nil` / `try/except` / `Result.Err`. Are errors propagated with context, swallowed, or logged-and-ignored?
3. **Concurrency.** Shared state without locks, goroutine leaks, missing cancellation, channel deadlocks.
4. **Resource lifecycle.** Files closed, connections released, contexts cancelled, timers stopped.

If a behavior changed, the test suite must change with it. No test diff for a logic change is itself a finding.

## Phase 3 — Security checklist

STOP and escalate (CRITICAL severity) if any of these appear:

- Hardcoded credentials, API keys, tokens, passwords
- SQL/command/template injection (string concatenation into queries or shells)
- XSS (unescaped user input rendered to HTML)
- Path traversal (user-supplied paths without sanitization)
- Missing auth on a state-changing endpoint
- Authentication bypasses (`if user == "admin" { skip }`)
- Cryptographic primitives rolled by hand or using deprecated algos (MD5, SHA-1 for security)

## Phase 4 — Code quality

- Functions > 50 lines: split candidates
- Files > 800 lines: extract module candidates
- Nesting depth > 4: early-return refactor candidates
- Magic numbers without named constants
- Mutation patterns where immutability is idiomatic
- Names that lie (`getUser` that also writes to disk)
- Dead code, unused imports, debug `console.log` / `fmt.Println`

## Phase 5 — Performance

Cheap to catch in review, expensive to catch in production:

- N+1 queries (loop with a query inside)
- Missing pagination / `LIMIT` on user-driven queries
- Unbounded growth (slices/maps that never shrink)
- Missing caching on expensive deterministic computations
- Sync I/O on hot paths

## Phase 6 — Verdict

Categorize each finding:

| Level | Meaning | Action |
|-------|---------|--------|
| CRITICAL | Security vuln or data loss risk | BLOCK merge |
| HIGH | Bug or significant quality issue | WARN — fix before merge |
| MEDIUM | Maintainability concern | INFO — consider |
| LOW | Style or minor suggestion | NOTE — optional |

Approve only when no CRITICAL or HIGH issues remain.

## Output format

```
SUMMARY: <one sentence>

CRITICAL:
- <file>:<line> — <issue> — <suggested fix>

HIGH:
- <file>:<line> — <issue> — <suggested fix>

MEDIUM:
- <file>:<line> — <issue>

LOW:
- <file>:<line> — <issue>

VERDICT: approve | warn | block
```

Be specific. "This function is too long" is useless. "splitOrderItems on line 142 mixes parsing, validation, and persistence — split into parseOrder/validateOrder/saveOrder" is reviewable.
