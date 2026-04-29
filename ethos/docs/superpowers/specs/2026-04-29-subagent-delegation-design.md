# Sub-Agent System & Cross-Agent Delegation

> Phase 2 remaining: SubAgentManager, 4 built-in task types, external delegator, file-state coordination, cost rollup.

---

## 1. Problem

Hermes has an 800-line `delegate_tool.py` that spawns child agents in Python threads. Three issues:

1. **GIL-bound** — Python threads don't give true parallelism for CPU work. Goroutines do.
2. **No external delegation** — can't say "have Claude Code implement this." Only internal children.
3. **God function** — `_run_single_child()` mixes spawning, timeout handling, cost rollup, file-state tracking, diagnostics, and hook firing in one function.

Ethos fixes all three with goroutine-per-child, cross-agent delegation via subprocess, and clean separation of concerns.

---

## 2. Package Structure

```
internal/subagent/
├── manager.go          # SubAgentManager — spawner, depth tracking, registry
├── manager_test.go
├── task.go             # Task interface + built-in task types
├── task_test.go
├── worker.go           # Worker — goroutine-based child agent runner
├── worker_test.go
├── context.go          # Context passing (structured export/import)
├── context_test.go
├── external.go         # External delegator — spawns CLI agents as subprocesses
├── external_test.go
├── filestate.go        # File-state coordination (parent-read/child-write tracking)
├── filestate_test.go
├── cost.go             # Cost/token rollup from children into parent session
├── cost_test.go
```

---

## 3. Core Types

### 3.1 Role

```go
type Role int

const (
    RoleWorker       Role = iota // Cannot re-delegate (default)
    RoleOrchestrator             // Can spawn own workers, bounded by max depth
)
```

`RoleWorker` children have no `delegate_task` tool — they physically cannot re-delegate. `RoleOrchestrator` children get the tool but their SubAgentManager has `depth = parent.depth + 1`, so depth limit is naturally enforced.

### 3.2 Task

```go
type Task interface {
    Goal() string
    Context() string
    Toolset() []string
    Model() string
    MaxIterations() int
    Validate() error
}
```

Every task type validates itself before spawning. Empty goal, zero-length context for compaction, etc. — caught early, not mid-execution.

### 3.3 Result

```go
type Result struct {
    TaskIndex    int
    Status       string     // completed, failed, timeout, interrupted
    Summary      string     // Child's final output
    Error        string
    TokensIn     int64
    TokensOut    int64
    CostUSD      float64
    DurationMs   int64
    ToolTrace    []ToolEntry
    FilesRead    []string
    FilesWritten []string
    ExitReason   string     // completed, max_iterations, timeout, error
}
```

### 3.4 SubAgentManager

```go
type SubAgentManager struct {
    mu           sync.RWMutex
    depth        int
    maxDepth     int                  // default 2
    maxChildren  int                  // default 3
    children     map[string]*ChildRef
    sessionStore session.Store
    provider     providers.Provider
    toolRegistry *tools.Registry
    hooks        *hooks.Registry
    tokenizer    *tokenizer.Estimator
    fileState    *FileStateTracker
    costTracker  *CostRollup
}

type ChildRef struct {
    ID        string
    Goal      string
    Model     string
    Status    string             // running, completed, failed, timeout
    StartedAt time.Time
    Cancel    context.CancelFunc
    Result    *Result
    Depth     int
    Role      Role
}
```

---

## 4. Spawning Mechanics

### 4.1 Spawn (single child)

```go
func (m *SubAgentManager) Spawn(ctx context.Context, task Task) (*Result, error)
```

1. **Depth check** — if `m.depth >= m.maxDepth`, reject with error message showing current depth and configured max.
2. **Capacity check** — if `len(m.children) >= m.maxChildren`, reject with "too many concurrent children."
3. **Build child agent** — new `agent.Agent` with own session (ParentID = parent session ID), subset of tools, optional model override, fresh conversation history, reduced maxSteps (default 15).
4. **Register child** — add to `m.children` map with cancel func.
5. **Launch goroutine** — run child's ReAct loop with `context.WithTimeout` (default 120s), cancellation from parent ctx, progress callbacks via channel (non-blocking).
6. **Await result** — goroutine writes to result channel, manager collects.
7. **Rollup** — fold child's tokens/cost into parent session via CostRollup.
8. **File-state check** — if child wrote to files parent previously read, attach warning to result summary.
9. **Unregister child** — remove from active map.

### 4.2 SpawnBatch (parallel children)

```go
func (m *SubAgentManager) SpawnBatch(ctx context.Context, tasks []Task) ([]*Result, error)
```

- Uses `errgroup.Group` with `SetLimit(m.maxChildren)` for bounded parallelism.
- All children share parent's context — if parent is interrupted, all children get cancelled.
- Results sorted by TaskIndex to match input order.
- Individual child failure doesn't cancel siblings.

### 4.3 Timeouts

- Per-child: 120s default, configurable via `Config.ChildTimeout`.
- 0-API-call diagnostic: if child times out before making any LLM call, result includes diagnostic info (stuck in prompt construction? credential resolution? transport issue?).
- No heartbeat thread — cancellation is purely context-based.

### 4.4 Interrupt Propagation

- Parent context cancelled → all children cancelled via derived context.
- Individual child cancellable via `ChildRef.Cancel()`.
- Graceful: child gets 5s to finish current tool call before force-kill.

### 4.5 Config

```toml
[delegation]
max_depth = 2
max_children = 3
child_timeout = "120s"
orchestrator_enabled = true
```

---

## 5. Built-In Task Types

### 5.1 CompactionTask

Summarizes old context, returns compressed version. Used by compaction engine for LLM-based summarization.

```go
type CompactionTask struct {
    Messages     []providers.Message
    TargetTokens int
    Model        string // default: cheapest available
}
```

- Goal: "Summarize the following conversation history into a concise summary preserving all decisions, code patterns, and important context."
- Toolset: none (pure LLM call, no tools).
- Model: cheapest available by default.
- Validation: fails if Messages is empty or TargetTokens < 100.

### 5.2 SessionNameTask

Generates a session title from the user's first message. 80 max tokens.

```go
type SessionNameTask struct {
    FirstMessage string
    MaxTokens    int // default 80
}
```

- Goal: "Generate a concise session title (max 10 words) that captures the main topic."
- Toolset: none.
- Model: cheapest available.
- Validation: fails if FirstMessage is empty.

### 5.3 PersonalityUpdateTask

Analyzes session for relationship beats, mood shifts, milestones.

```go
type PersonalityUpdateTask struct {
    SessionMessages []providers.Message
    CurrentLevel    personality.Level
    AgentName       string
}
```

- Goal: "Analyze this conversation for relationship milestones, emotional beats, and interaction patterns."
- Toolset: none.
- Model: cheapest available.
- Returns structured JSON: `{beats: [], mood: string, suggestions: []}`.
- Validation: fails if SessionMessages is empty.

### 5.4 VisualDebugTask

Stub for Phase 4 (frontend screenshot comparison via vision model).

```go
type VisualDebugTask struct {
    ScreenshotPath string
    ExpectedPath   string
}
```

- Goal: "Compare these two screenshots and report visual differences."
- Toolset: `["fs"]` (file reading only).
- Model: vision-capable model (optional, falls back to main model).
- MVP: returns error "visual debugging not yet available — coming in Phase 4."
- Validation: fails if ScreenshotPath is empty. Does NOT fail for missing file (defers to runtime).

---

## 6. External Delegator

Spawns external CLI agents (Claude Code, OpenCode, Codex) as subprocesses. Structured context export/import.

### 6.1 Types

```go
type ExternalDelegator struct {
    agents    map[string]AgentDef
    workDir   string
    timeout   time.Duration
    fileState *FileStateTracker
}

type AgentDef struct {
    Name     string
    Command  string
    Args     []string
    Protocol Protocol
    Model    string
    Env      map[string]string
}

type Protocol int

const (
    ProtocolStdio Protocol = iota // stdin prompt, stdout result
    ProtocolACP                   // Agent Control Protocol (JSON-RPC)
    ProtocolPipe                  // file-based: write prompt to temp, read result
)
```

### 6.2 Context Export Format

When delegating externally, Ethos exports a structured JSON envelope:

```json
{
  "session_id": "abc-123",
  "goal": "Implement JWT auth module with refresh tokens",
  "context": {
    "files_modified": ["internal/auth/jwt.go"],
    "recent_changes": "...git diff --stat output...",
    "project_structure": "...tree -L 2...",
    "constraints": "Must use BadgerDB. No external deps.",
    "language": "Go",
    "related_conversation": "...last 5 relevant messages..."
  },
  "ethos_version": "0.1.0"
}
```

Secrets are filtered by the security scanner before export. No API keys, no credentials.

### 6.3 Execution Flow

1. Look up agent by name in registered agents map.
2. Check command exists in PATH — return clear error if not found.
3. Export context to JSON, filter secrets.
4. Spawn subprocess with context cancellation and timeout.
5. Pipe context via stdin, stream stdout.
6. Parse output back into `Result` struct.
7. Track file writes via FileStateTracker.
8. Fold cost estimate into parent session.

### 6.4 Pre-Registered Agents

| Name | Command | Args | Protocol |
|------|---------|------|----------|
| `claude-code` | `claude` | `--acp --stdio` | ACP |
| `opencode` | `opencode` | `-p` | stdio |
| `codex` | `codex` | `--quiet` | stdio |

Users can add more via config:

```toml
[delegation.external.claude-code]
command = "claude"
args = ["--acp", "--stdio"]
protocol = "acp"

[delegation.external.opencode]
command = "opencode"
args = ["-p"]
protocol = "stdio"
```

### 6.5 Safety

- Working directory scoped to project root — child cannot escape.
- No API keys or secrets in context export (security scanner filters).
- Timeout enforced via context cancellation.
- File writes tracked — parent gets warned if files changed.

---

## 7. File-State Coordination

Tracks who read what and who wrote what. Prevents silent conflicts when child agents modify files the parent was working with.

### 7.1 FileStateTracker

```go
type FileStateTracker struct {
    mu     sync.RWMutex
    reads  map[string][]string // taskID -> file paths read
    writes map[string][]string // taskID -> file paths written
}

func (t *FileStateTracker) RecordRead(taskID, path string)
func (t *FileStateTracker) RecordWrite(taskID, path string)
func (t *FileStateTracker) KnownReads(taskID string) []string
func (t *FileStateTracker) WritesSince(taskID string, since time.Time, knownReads []string) []string
```

### 7.2 Conflict Detection

After any child completes (internal or external):

1. Get parent's known reads: `fileState.KnownReads(parentTaskID)`
2. Get child's writes since spawn time: `fileState.WritesSince(parentTaskID, childStartTime, parentReads)`
3. If overlap found, append warning to child's result summary:
   ```
   [NOTE: subagent modified files the parent previously read — re-read before editing: auth.go, middleware.go]
   ```

### 7.3 Integration Points

- `internal/tools/fs` — RecordRead on every file read tool call.
- `internal/tools/shell` — RecordWrite on detected file modifications (git diff based).
- `SubAgentManager.Spawn` — checks WritesSince after child completes.
- `ExternalDelegator.Delegate` — same check after external agent finishes.

---

## 8. Cost Rollup

Folds child agent spend into parent session atomically.

### 8.1 CostRollup

```go
type CostRollup struct {
    mu           sync.Mutex
    sessionID    string
    store        session.Store
    childrenIn   int64
    childrenOut  int64
    childrenCost float64
}

func (c *CostRollup) AddChild(result *Result) error
func (c *CostRollup) Summary() RollupSummary

type RollupSummary struct {
    ChildrenCount int
    TotalIn       int64
    TotalOut      int64
    TotalCost     float64
}
```

### 8.2 Integration

- After each child completes, `CostRollup.AddChild(result)` is called.
- Updates parent session's `TokenCount` and `CostUSD` in BadgerDB.
- TUI status bar picks up the update via existing `CostUpdateMsg` — no special wiring needed.
- If parent has no direct API calls this turn, cost source is tagged `"subagent"` so the UI labels it correctly.
- Nested delegation rolls up naturally: each layer folds its direct children, and when the orchestrator finishes, its parent folds the now-inflated total.

---

## 9. Testing Strategy

TDD enforced. Tests written first. Target: ~60 tests across the package.

| File | Tests | What |
|------|-------|------|
| `manager_test.go` | ~15 | Spawn, SpawnBatch, depth limits, capacity limits, cancellation, interrupt propagation |
| `task_test.go` | ~12 | Each task type's Validate, Goal, Context, Toolset, Model, MaxIterations |
| `worker_test.go` | ~8 | Goroutine execution, timeout, 0-API-call diagnostic, result collection |
| `context_test.go` | ~6 | Context export format, secret filtering, JSON round-trip |
| `external_test.go` | ~10 | Agent lookup, PATH check, subprocess spawn, timeout, result parsing, missing command |
| `filestate_test.go` | ~6 | RecordRead, RecordWrite, KnownReads, WritesSince, conflict detection |
| `cost_test.go` | ~5 | AddChild, Summary, nested rollup, atomic updates |

---

## 10. What We Take From Hermes (And Improve)

| Hermes | Ethos Improvement |
|--------|-------------------|
| Python threads (GIL-bound) | Goroutines (true parallelism) |
| 800-line god function | Clean separation: manager, worker, filestate, cost, external |
| Global state mutation (`model_tools._last_resolved_tool_names`) | No global state — each child is a self-contained Agent |
| Heartbeat thread | Context-based cancellation only |
| No external delegation | ExternalDelegator with structured context export |
| String-only context field | Structured JSON envelope with secret filtering |
| `ThreadPoolExecutor` | `errgroup.Group` with `SetLimit` |
| In-code constants for limits | TOML-configurable limits |

---

## 11. Integration With Agent Loop

The agent loop (`internal/agent/`) gets one new tool: `delegate_task`.

```go
// internal/tools/delegate.go
// Registered in the tool registry like any other tool.
// The agent's ReAct loop calls it like shell, fs, grep, etc.

// delegate_task parameters:
//   goal (required): what the sub-agent should accomplish
//   context (optional): background info for the child
//   tasks (optional): batch mode — array of {goal, context, toolsets, role}
//   toolsets (optional): which tools the child gets
//   role (optional): "worker" (default) or "orchestrator"
//   model (optional): override child's model
//   external (optional): name of external agent (e.g. "claude-code")

// The tool handler:
// 1. If external is set → ExternalDelegator.Delegate()
// 2. If tasks array → SubAgentManager.SpawnBatch()
// 3. Otherwise → SubAgentManager.Spawn() with single task
```

The SubAgentManager is injected into the agent at construction time via `Config`:

```go
type Config struct {
    // ... existing fields ...
    SubAgentManager *subagent.SubAgentManager // nil = delegation disabled
}
```

When nil, the `delegate_task` tool returns an error: "Delegation is not configured." This allows running Ethos without sub-agents in minimal mode.

---

## 12. Out of Scope (Deferred)

- ACP protocol implementation detail (Phase 4 — when browser tools land)
- Visual debug task actual implementation (Phase 4 stub only)
- MCP-based agent-to-agent communication (Phase 5)
- Agent capability negotiation (agents advertising what they can do)
- Result verification by parent (post-delegation verification that child's claimed output is real) — will be added as a follow-up
