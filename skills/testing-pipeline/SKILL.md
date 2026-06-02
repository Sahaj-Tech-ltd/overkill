---
name: testing-pipeline
description: Use when running tests, checking coverage, running mutation tests, or using the Overkill test panel. Also use when the user says "test this", "run tests", "what's coverage", or "mutation test".
---

# Testing Pipeline

## Overview

Overkill has a multi-layer test pipeline: Go tests, TypeScript typecheck, Python bridge tests, and mutation testing. The TUI sidebar exposes a Test Panel for visual feedback.

**For full codebase audits:** Load `bug-hunt` skill — the 8-category checklist that finds what narrow prompts miss (compile blockers, security, data integrity, design flaws, silent failures). The checklist IS the skill.

## Quick Commands

```bash
cd ~/docker/overkill

# Go tests (all packages)
go test ./...

# Go tests with race detector
go test -race ./...

# Specific package
go test -run TestAgent ./internal/agent/

# Coverage
go test -cover ./...

# TypeScript typecheck
cd tui && npx tsc --noEmit

# Python bridge tests
cd bridge && pytest
```

## Mutation Testing

```bash
cd ~/docker/overkill

# Install if missing
go install github.com/zimmski/go-mutesting/cmd/go-mutesting@latest

# Run on a package
go-mutesting ./internal/agent/

# Results: survived mutants = weak tests, killed = good tests
```

Mutation score = killed / (killed + survived + skipped). Aim for >80%.

## Coverage Interpretation

- <50% — red flag, add tests
- 50-80% — decent, look at uncovered branches
- >80% — good, focus on mutation score

## Test Panel (TUI)

The TUI sidebar has a "Tests" tab that shows:
- Last test run results
- Coverage percentage
- Failed tests with file:line references

Trigger a test run from the panel or via the API:
```bash
curl -X POST http://localhost:<port>/rpc -d '{"method":"tests.run","params":{}}'
```

## When Tests Fail

1. Read the failure output carefully — exact file:line
2. Don't guess — reproduce locally with the same `go test -run` command
3. Fix the root cause, not the symptom
4. Run the full suite after fixing — one fix can break another test

## Adding New Tests

Go: `_test.go` in the same package as the code under test.
Pattern: table-driven tests with subtests.

```go
func TestThing(t *testing.T) {
    tests := []struct {
        name string
        input string
        want string
    }{
        {"empty", "", ""},
        {"normal", "hello", "HELLO"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := DoThing(tt.input)
            if got != tt.want {
                t.Errorf("got %q, want %q", got, tt.want)
            }
        })
    }
}
```
