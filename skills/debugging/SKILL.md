---
name: debugging
description: Use when encountering any bug, test failure, unexpected behavior, crash, or error — before attempting any fix. Also use when the user says "debug this", "why is this broken", or "find the bug".
---

# Systematic Debugging

## Overview

4-phase root-cause debugging. **Never fix before understanding.** Most bugs get worse when you patch symptoms without finding the root cause.

## Phase 1: Reproduce

Make the bug happen reliably. If you can't reproduce it, you can't fix it.

- Run the failing test: `go test -run TestName ./pkg/`
- Run the exact command that triggered it
- Note the exact input, environment, and state

## Phase 2: Isolate

Narrow down WHERE the bug lives. Binary search the codebase.

- `git bisect` if it's a regression
- Comment out half the pipeline, see if bug persists
- Add logging/print statements at suspect points
- Trace the data flow: input → transform → output — where does it go wrong?

## Phase 3: Understand

Answer these questions BEFORE touching code:
1. What exact line/condition causes the wrong behavior?
2. What should it do instead?
3. Why was it written this way originally? (git blame)
4. What ELSE depends on this behavior? (callers, tests, configs)

**Red flag:** If you can't explain the bug to a junior dev in 3 sentences, you don't understand it yet.

## Phase 4: Fix

Minimal diff. One concern per fix.

1. Write a failing test that captures the bug
2. Make the smallest change that makes the test pass
3. Run the full test suite
4. Verify the original reproduction no longer triggers

## Common Patterns

### Nil pointer / nil map
```go
// ❌ panic
var m map[string]int
m["key"] = 1

// ✅ initialize or check
m = make(map[string]int)
```

### Goroutine leak
```go
// ❌ goroutine lives forever
go func() {
    for { doWork() }
}()

// ✅ context cancellation
ctx, cancel := context.WithCancel(parent)
go func() {
    for {
        select {
        case <-ctx.Done():
            return
        default:
            doWork()
        }
    }
}()
```

### Race condition
Run with `-race` flag: `go test -race ./...`
The race detector is not 100% but a flagged race is always a real bug.

## Debugging Tools

- `go test -race ./...` — race detector
- `go test -run TestX -v` — verbose single test
- `dlv debug` — Delve debugger (breakpoints, step-through)
- `print`/`fmt.Printf` — sometimes the fastest path
- `git log -p -- path/to/file` — see what changed
