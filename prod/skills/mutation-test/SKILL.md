---
name: mutation-test
version: 1.0.0
description: Verify that a test suite actually catches bugs by mutating the production code and checking which mutants survive. Use when test coverage is high but bugs still slip through, or to validate a TDD'd module before shipping.
author: overkill-team
category: testing
tags: [testing, mutation, quality, regression]
triggers: [mutation, "mutation test", "is my test suite real", "verify test quality"]
enabled: true
---

# Mutation Test

Coverage tells you which lines ran. It doesn't tell you which lines are *checked*. A test that runs a function and asserts nothing covers the line and proves nothing.

Mutation testing fixes that: deliberately introduce a bug (mutation), run the suite, and check whether any test fails. If no test fails, the bug survived — the suite has a hole.

## When to use

- Module is critical (auth, payments, data integrity, scheduler)
- Coverage is high (≥ 80%) but bugs still appear in production
- Right after a TDD round, to verify the tests assert what you think
- During code review of a test PR (asks: do these tests actually test anything?)

## When NOT to use

- Exploratory or prototype code
- Glue code with no real logic
- Where the slow runtime cost outweighs the value

## Standard mutation operators

Apply each to one site at a time:

### Conditional boundary
- `>` → `>=` and vice versa
- `<` → `<=` and vice versa

### Negate conditional
- `==` → `!=`
- `>` → `<=`
- `&&` → `||`

### Math
- `+` → `-`
- `*` → `/`
- `++` → `--`

### Return value
- `return x` → `return null` / `return ""` / `return 0`
- `return true` → `return false`

### Boundary
- Off-by-one: `i < n` → `i <= n`
- Empty input handling: comment out the early return

### Constant
- Numeric literals: `1` → `0`, `100` → `99`
- String literals: change one character
- Bool literals: `true` ↔ `false`

### Statement deletion
- Delete a single statement (especially `if` guards, validation, error returns)

### Method call
- Comment out a method call, especially logging, audit, side-effect

## Process

1. **Pick one operator and one site.** Do not mutate randomly across the file.
2. **Apply the mutation.** Single-character edit usually.
3. **Run the test suite.** With `--fail-fast` if available.
4. **Record the outcome:**
   - **Killed** — at least one test failed. Good. Suite caught it.
   - **Survived** — all tests passed. Bad. Hole in the suite.
   - **Equivalent** — mutation produces semantically identical code (e.g., `++i` vs `i++` in a context where order doesn't matter). Skip.
   - **Timeout** — mutation caused infinite loop. Treat as killed if the suite has a timeout guard; otherwise mark for manual review.
5. **Revert the mutation.** Always.
6. **For each surviving mutant: write a new test that kills it.** This is the entire point.

## Tooling

| Language | Tool |
|----------|------|
| Java/Kotlin | PIT |
| JavaScript/TypeScript | Stryker |
| Python | mutmut, cosmic-ray |
| Go | gremlins, go-mutesting |
| Ruby | mutant |
| Rust | mutagen, cargo-mutants |
| C/C++ | mull, dextool-mutate |

If no automated tool is available or it's overkill for the scope, the manual process above is fine for a focused module.

## Targets

A healthy mutation score for a critical module: **≥ 80% killed**.

Below 60%: the test suite is performative. Hidden bugs are very likely.

100% is usually impossible (equivalent mutants exist). Aim for 80–95%.

## Output format

```
TARGET: <file or module>
TOTAL MUTATIONS: <n>
KILLED: <n> (<%>)
SURVIVED: <n>
EQUIVALENT: <n>

SURVIVING MUTANTS:

1. <file>:<line>
   Original: <snippet>
   Mutated:  <snippet>
   Why no test caught it: <one sentence>
   Suggested test: <description>

2. ...

VERDICT: pass | weak | unsafe
```

## Anti-patterns

- Reporting the mutation score without listing which mutants survived (the survivors *are* the value)
- Mutating multiple sites at once (you can't tell which one slipped past the suite)
- Forgetting to revert (commit a mutation = ship a bug)
