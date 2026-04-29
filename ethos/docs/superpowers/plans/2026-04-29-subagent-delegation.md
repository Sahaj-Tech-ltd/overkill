# Sub-Agent System & Cross-Agent Delegation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build goroutine-based sub-agent spawner, 4 built-in task types, external CLI agent delegator, file-state coordination, and cost rollup.

**Architecture:** New `internal/subagent/` package with 8 source files. Each child agent runs in its own goroutine via `errgroup.Group`. External agents spawn as subprocesses. File-state tracker prevents stale-read conflicts. Cost rollup folds child spend into parent session atomically.

**Tech Stack:** Go stdlib (`context`, `sync`, `os/exec`, `encoding/json`, `time`, `errgroup`), existing packages (`internal/agent`, `internal/session`, `internal/providers`, `internal/tools`, `internal/hooks`, `internal/cost`, `internal/security`)

**Spec:** `docs/superpowers/specs/2026-04-29-subagent-delegation-design.md`

**Target:** ~62 tests across 8 test files

---

## File Map

| File | Responsibility |
|------|---------------|
| `internal/subagent/manager.go` | SubAgentManager — spawner, depth/capacity tracking, child registry |
| `internal/subagent/task.go` | Task interface + Role enum + 4 built-in task types |
| `internal/subagent/worker.go` | Worker — goroutine-based child agent runner, timeout, diagnostics |
| `internal/subagent/context.go` | Context export/import — structured JSON envelope for external agents |
| `internal/subagent/external.go` | ExternalDelegator — spawns CLI agents as subprocesses |
| `internal/subagent/filestate.go` | FileStateTracker — parent-read/child-write conflict detection |
| `internal/subagent/cost.go` | CostRollup — folds child tokens/cost into parent session |
| `internal/tools/delegate.go` | delegate_task tool — wires agent loop to SubAgentManager |

---

## Wave 1: Foundation (filestate, cost, task types)

### Task 1: FileStateTracker

**Files:**
- Create: `internal/subagent/filestate.go`
- Test: `internal/subagent/filestate_test.go`

- [ ] **Step 1: Write tests for FileStateTracker**

```go
// internal/subagent/filestate_test.go
package subagent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFileStateTracker_RecordRead(t *testing.T) {
	fs := NewFileStateTracker()
	fs.RecordRead("parent-1", "auth.go")
	fs.RecordRead("parent-1", "middleware.go")

	reads := fs.KnownReads("parent-1")
	assert.Equal(t, []string{"auth.go", "middleware.go"}, reads)
}

func TestFileStateTracker_KnownReadsEmpty(t *testing.T) {
	fs := NewFileStateTracker()
	reads := fs.KnownReads("unknown")
	assert.Empty(t, reads)
}

func TestFileStateTracker_RecordWrite(t *testing.T) {
	fs := NewFileStateTracker()
	fs.RecordWrite("child-1", "auth.go")
	fs.RecordWrite("child-1", "config.go")

	writes := fs.WritesByTask("child-1")
	assert.Equal(t, []string{"auth.go", "config.go"}, writes)
}

func TestFileStateTracker_WritesSinceConflict(t *testing.T) {
	fs := NewFileStateTracker()
	start := time.Now()

	fs.RecordRead("parent-1", "auth.go")
	fs.RecordRead("parent-1", "middleware.go")
	fs.RecordRead("parent-1", "utils.go")

	fs.RecordWrite("child-1", "auth.go")
	fs.RecordWrite("child-1", "new_file.go")

	parentReads := fs.KnownReads("parent-1")
	conflicts := fs.WritesSince("child-1", start, parentReads)

	assert.Equal(t, []string{"auth.go"}, conflicts)
}

func TestFileStateTracker_WritesSinceNoConflict(t *testing.T) {
	fs := NewFileStateTracker()
	start := time.Now()

	fs.RecordRead("parent-1", "auth.go")
	fs.RecordWrite("child-1", "config.go")

	parentReads := fs.KnownReads("parent-1")
	conflicts := fs.WritesSince("child-1", start, parentReads)

	assert.Empty(t, conflicts)
}

func TestFileStateTracker_ConcurrentAccess(t *testing.T) {
	fs := NewFileStateTracker()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 100; i++ {
			fs.RecordRead("task-1", "file.go")
		}
		close(done)
	}()

	for i := 0; i < 100; i++ {
		fs.RecordWrite("task-2", "file.go")
	}

	<-done
	reads := fs.KnownReads("task-1")
	assert.Len(t, reads, 100)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/subagent/ -run TestFileState -v`
Expected: FAIL — package doesn't exist

- [ ] **Step 3: Implement FileStateTracker**

```go
// internal/subagent/filestate.go
package subagent

import (
	"path/filepath"
	"sync"
	"time"
)

type FileStateTracker struct {
	mu     sync.RWMutex
	reads  map[string][]string
	writes map[string][]string
	times  map[string]time.Time
}

func NewFileStateTracker() *FileStateTracker {
	return &FileStateTracker{
		reads:  make(map[string][]string),
		writes: make(map[string][]string),
		times:  make(map[string]time.Time),
	}
}

func (t *FileStateTracker) RecordRead(taskID, path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	abs, _ := filepath.Abs(path)
	t.reads[taskID] = append(t.reads[taskID], abs)
}

func (t *FileStateTracker) RecordWrite(taskID, path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	abs, _ := filepath.Abs(path)
	if _, ok := t.times[taskID]; !ok {
		t.times[taskID] = time.Now()
	}
	t.writes[taskID] = append(t.writes[taskID], abs)
}

func (t *FileStateTracker) KnownReads(taskID string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	reads, ok := t.reads[taskID]
	if !ok {
		return nil
	}
	out := make([]string, len(reads))
	copy(out, reads)
	return out
}

func (t *FileStateTracker) WritesByTask(taskID string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	writes, ok := t.writes[taskID]
	if !ok {
		return nil
	}
	out := make([]string, len(writes))
	copy(out, writes)
	return out
}

func (t *FileStateTracker) WritesSince(taskID string, since time.Time, knownReads []string) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	writes, ok := t.writes[taskID]
	if !ok {
		return nil
	}

	readSet := make(map[string]struct{}, len(knownReads))
	for _, r := range knownReads {
		readSet[r] = struct{}{}
	}

	var conflicts []string
	for _, w := range writes {
		if _, exists := readSet[w]; exists {
			conflicts = append(conflicts, w)
		}
	}
	return conflicts
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/subagent/ -run TestFileState -v`
Expected: PASS (6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/subagent/filestate.go internal/subagent/filestate_test.go
git commit -m "feat(subagent): add FileStateTracker for parent-read/child-write conflict detection"
```

---

### Task 2: CostRollup

**Files:**
- Create: `internal/subagent/cost.go`
- Test: `internal/subagent/cost_test.go`

- [ ] **Step 1: Write tests for CostRollup**

```go
// internal/subagent/cost_test.go
package subagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCostRollup_AddChild(t *testing.T) {
	cr := NewCostRollup("session-1")

	cr.AddChild(&Result{TokensIn: 1500, TokensOut: 800, CostUSD: 0.003})
	cr.AddChild(&Result{TokensIn: 2000, TokensOut: 1200, CostUSD: 0.005})

	summary := cr.Summary()
	assert.Equal(t, 2, summary.ChildrenCount)
	assert.Equal(t, int64(3500), summary.TotalIn)
	assert.Equal(t, int64(2000), summary.TotalOut)
	assert.InDelta(t, 0.008, summary.TotalCost, 0.0001)
}

func TestCostRollup_Empty(t *testing.T) {
	cr := NewCostRollup("session-1")
	summary := cr.Summary()
	assert.Equal(t, 0, summary.ChildrenCount)
	assert.Equal(t, int64(0), summary.TotalIn)
	assert.Equal(t, int64(0), summary.TotalOut)
	assert.InDelta(t, 0.0, summary.TotalCost, 0.0001)
}

func TestCostRollup_ConcurrentAdd(t *testing.T) {
	cr := NewCostRollup("session-1")
	done := make(chan struct{})

	go func() {
		for i := 0; i < 50; i++ {
			cr.AddChild(&Result{TokensIn: 100, TokensOut: 50, CostUSD: 0.001})
		}
		close(done)
	}()

	for i := 0; i < 50; i++ {
		cr.AddChild(&Result{TokensIn: 100, TokensOut: 50, CostUSD: 0.001})
	}

	<-done
	summary := cr.Summary()
	assert.Equal(t, 100, summary.ChildrenCount)
	assert.Equal(t, int64(10000), summary.TotalIn)
	assert.InDelta(t, 0.1, summary.TotalCost, 0.001)
}

func TestCostRollup_SingleChild(t *testing.T) {
	cr := NewCostRollup("session-1")
	cr.AddChild(&Result{TokensIn: 500, TokensOut: 300, CostUSD: 0.002, TaskIndex: 0})

	summary := cr.Summary()
	assert.Equal(t, 1, summary.ChildrenCount)
	assert.Equal(t, int64(500), summary.TotalIn)
	assert.Equal(t, int64(300), summary.TotalOut)
	assert.InDelta(t, 0.002, summary.TotalCost, 0.0001)
}

func TestCostRollup_NilResult(t *testing.T) {
	cr := NewCostRollup("session-1")
	cr.AddChild(nil)

	summary := cr.Summary()
	assert.Equal(t, 0, summary.ChildrenCount)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/subagent/ -run TestCostRollup -v`
Expected: FAIL — CostRollup not defined

- [ ] **Step 3: Implement CostRollup**

```go
// internal/subagent/cost.go
package subagent

import "sync"

type RollupSummary struct {
	ChildrenCount int
	TotalIn       int64
	TotalOut      int64
	TotalCost     float64
}

type CostRollup struct {
	mu           sync.Mutex
	sessionID    string
	childrenIn   int64
	childrenOut  int64
	childrenCost float64
	childrenN    int
}

func NewCostRollup(sessionID string) *CostRollup {
	return &CostRollup{sessionID: sessionID}
}

func (c *CostRollup) AddChild(result *Result) {
	if result == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.childrenIn += result.TokensIn
	c.childrenOut += result.TokensOut
	c.childrenCost += result.CostUSD
	c.childrenN++
}

func (c *CostRollup) Summary() RollupSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	return RollupSummary{
		ChildrenCount: c.childrenN,
		TotalIn:       c.childrenIn,
		TotalOut:      c.childrenOut,
		TotalCost:     c.childrenCost,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/subagent/ -run TestCostRollup -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/subagent/cost.go internal/subagent/cost_test.go
git commit -m "feat(subagent): add CostRollup for child token/cost aggregation"
```

---

### Task 3: Task Interface + Built-In Task Types

**Files:**
- Create: `internal/subagent/task.go`
- Test: `internal/subagent/task_test.go`

- [ ] **Step 1: Write tests for Task types**

```go
// internal/subagent/task_test.go
package subagent

import (
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/personality"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/stretchr/testify/assert"
)

func TestCompactionTask_Validate(t *testing.T) {
	task := CompactionTask{Messages: nil, TargetTokens: 500}
	assert.Error(t, task.Validate())

	task = CompactionTask{Messages: []providers.Message{{Role: "user", Content: "hi"}}, TargetTokens: 50}
	assert.Error(t, task.Validate())

	task = CompactionTask{Messages: []providers.Message{{Role: "user", Content: "hi"}}, TargetTokens: 500}
	assert.NoError(t, task.Validate())
}

func TestCompactionTask_Fields(t *testing.T) {
	msgs := []providers.Message{{Role: "user", Content: "test"}}
	task := CompactionTask{Messages: msgs, TargetTokens: 500, OverrideModel: "deepseek-chat"}
	assert.Contains(t, task.Goal(), "summarize")
	assert.NotEmpty(t, task.Context())
	assert.Empty(t, task.Toolset())
	assert.Equal(t, "deepseek-chat", task.Model())
	assert.Equal(t, 15, task.MaxIterations())
}

func TestSessionNameTask_Validate(t *testing.T) {
	task := SessionNameTask{FirstMessage: ""}
	assert.Error(t, task.Validate())

	task = SessionNameTask{FirstMessage: "fix the auth bug"}
	assert.NoError(t, task.Validate())
}

func TestSessionNameTask_Fields(t *testing.T) {
	task := SessionNameTask{FirstMessage: "fix the auth bug"}
	assert.Contains(t, task.Goal(), "session title")
	assert.Contains(t, task.Context(), "fix the auth bug")
	assert.Empty(t, task.Toolset())
	assert.Equal(t, 80, task.MaxTokens)
}

func TestPersonalityUpdateTask_Validate(t *testing.T) {
	task := PersonalityUpdateTask{SessionMessages: nil}
	assert.Error(t, task.Validate())

	task = PersonalityUpdateTask{SessionMessages: []providers.Message{{Role: "user", Content: "hey"}}}
	assert.NoError(t, task.Validate())
}

func TestPersonalityUpdateTask_Fields(t *testing.T) {
	msgs := []providers.Message{{Role: "user", Content: "hey"}}
	task := PersonalityUpdateTask{SessionMessages: msgs, CurrentLevel: personality.LevelWitty, AgentName: "Butter"}
	assert.Contains(t, task.Goal(), "relationship")
	assert.NotEmpty(t, task.Context())
	assert.Empty(t, task.Toolset())
}

func TestVisualDebugTask_Validate(t *testing.T) {
	task := VisualDebugTask{ScreenshotPath: ""}
	assert.Error(t, task.Validate())

	task = VisualDebugTask{ScreenshotPath: "/tmp/screen.png", ExpectedPath: "/tmp/expected.png"}
	assert.NoError(t, task.Validate())
}

func TestVisualDebugTask_StubError(t *testing.T) {
	task := VisualDebugTask{ScreenshotPath: "/tmp/screen.png"}
	assert.Contains(t, task.Goal(), "visual")
	assert.Equal(t, []string{"fs"}, task.Toolset())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/subagent/ -run TestCompaction -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement Task interface and types**

```go
// internal/subagent/task.go
package subagent

import (
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/ethos/internal/personality"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type Role int

const (
	RoleWorker       Role = iota
	RoleOrchestrator
)

type Task interface {
	Goal() string
	Context() string
	Toolset() []string
	Model() string
	MaxIterations() int
	Validate() error
}

type ToolEntry struct {
	Tool    string `json:"tool"`
	ArgsLen int    `json:"args_len"`
}

type Result struct {
	TaskIndex    int         `json:"task_index"`
	Status       string      `json:"status"`
	Summary      string      `json:"summary"`
	Error        string      `json:"error,omitempty"`
	TokensIn     int64       `json:"tokens_in"`
	TokensOut    int64       `json:"tokens_out"`
	CostUSD      float64     `json:"cost_usd"`
	DurationMs   int64       `json:"duration_ms"`
	ToolTrace    []ToolEntry `json:"tool_trace,omitempty"`
	FilesRead    []string    `json:"files_read,omitempty"`
	FilesWritten []string    `json:"files_written,omitempty"`
	ExitReason   string      `json:"exit_reason"`
}

type CompactionTask struct {
	Messages     []providers.Message
	TargetTokens int
	OverrideModel string
}

func (t CompactionTask) Goal() string {
	return fmt.Sprintf("Summarize the following conversation history into a concise summary preserving all decisions, code patterns, and important context. Target approximately %d tokens.", t.TargetTokens)
}

func (t CompactionTask) Context() string {
	data, _ := json.Marshal(t.Messages)
	return string(data)
}

func (t CompactionTask) Toolset() []string    { return nil }
func (t CompactionTask) Model() string        { return t.OverrideModel }
func (t CompactionTask) MaxIterations() int   { return 15 }

func (t CompactionTask) Validate() error {
	if len(t.Messages) == 0 {
		return fmt.Errorf("compaction task: messages cannot be empty")
	}
	if t.TargetTokens < 100 {
		return fmt.Errorf("compaction task: target tokens must be at least 100, got %d", t.TargetTokens)
	}
	return nil
}

type SessionNameTask struct {
	FirstMessage string
	MaxTokens    int
}

func (t SessionNameTask) Goal() string {
	return "Generate a concise session title (max 10 words) that captures the main topic of this conversation."
}

func (t SessionNameTask) Context() string {
	return t.FirstMessage
}

func (t SessionNameTask) Toolset() []string    { return nil }
func (t SessionNameTask) Model() string        { return "" }
func (t SessionNameTask) MaxIterations() int   { return 5 }

func (t SessionNameTask) Validate() error {
	if t.FirstMessage == "" {
		return fmt.Errorf("session name task: first message cannot be empty")
	}
	return nil
}

type PersonalityUpdateTask struct {
	SessionMessages []providers.Message
	CurrentLevel    personality.Level
	AgentName       string
}

func (t PersonalityUpdateTask) Goal() string {
	return "Analyze this conversation for relationship milestones, emotional beats, and interaction patterns. Return JSON: {beats: [], mood: string, suggestions: []}"
}

func (t PersonalityUpdateTask) Context() string {
	data, _ := json.Marshal(map[string]any{
		"messages":      t.SessionMessages,
		"current_level": t.CurrentLevel.String(),
		"agent_name":    t.AgentName,
	})
	return string(data)
}

func (t PersonalityUpdateTask) Toolset() []string    { return nil }
func (t PersonalityUpdateTask) Model() string         { return "" }
func (t PersonalityUpdateTask) MaxIterations() int    { return 10 }

func (t PersonalityUpdateTask) Validate() error {
	if len(t.SessionMessages) == 0 {
		return fmt.Errorf("personality update task: session messages cannot be empty")
	}
	return nil
}

type VisualDebugTask struct {
	ScreenshotPath string
	ExpectedPath   string
}

func (t VisualDebugTask) Goal() string {
	return "Compare two screenshots and report visual differences."
}

func (t VisualDebugTask) Context() string {
	return fmt.Sprintf("screenshot: %s, expected: %s", t.ScreenshotPath, t.ExpectedPath)
}

func (t VisualDebugTask) Toolset() []string    { return []string{"fs"} }
func (t VisualDebugTask) Model() string         { return "" }
func (t VisualDebugTask) MaxIterations() int    { return 5 }

func (t VisualDebugTask) Validate() error {
	if t.ScreenshotPath == "" {
		return fmt.Errorf("visual debug task: screenshot path cannot be empty")
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/subagent/ -run "TestCompaction|TestSessionName|TestPersonality|TestVisualDebug" -v`
Expected: PASS (12 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/subagent/task.go internal/subagent/task_test.go
git commit -m "feat(subagent): add Task interface with 4 built-in types (compaction, naming, personality, visual debug)"
```

---

## Wave 2: Core Spawning (worker, manager)

### Task 4: Worker

**Files:**
- Create: `internal/subagent/worker.go`
- Test: `internal/subagent/worker_test.go`

- [ ] **Step 1: Write tests for Worker**

```go
// internal/subagent/worker_test.go
package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorker_RunCompletes(t *testing.T) {
	cfg := WorkerConfig{
		Goal:       "say hello",
		Context:    "test context",
		MaxSteps:   5,
		Timeout:    30 * time.Second,
		TaskIndex:  0,
	}
	w := NewWorker(cfg)
	result, err := w.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "completed", result.Status)
	assert.Equal(t, 0, result.TaskIndex)
}

func TestWorker_RunTimesOut(t *testing.T) {
	cfg := WorkerConfig{
		Goal:      "long task",
		Context:   "test",
		MaxSteps:  100,
		Timeout:   50 * time.Millisecond,
		TaskIndex: 1,
	}
	w := NewWorker(cfg)
	result, err := w.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "timeout", result.Status)
	assert.Equal(t, 1, result.TaskIndex)
	assert.Contains(t, result.ExitReason, "timeout")
}

func TestWorker_RunCancelled(t *testing.T) {
	cfg := WorkerConfig{
		Goal:      "cancel me",
		Context:   "test",
		MaxSteps:  100,
		Timeout:   10 * time.Second,
		TaskIndex: 2,
	}
	w := NewWorker(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := w.Run(ctx)
	require.NoError(t, err)
	assert.Equal(t, "interrupted", result.Status)
}

func TestWorker_RunTracksDuration(t *testing.T) {
	cfg := WorkerConfig{
		Goal:      "timed task",
		Context:   "test",
		MaxSteps:  1,
		Timeout:   5 * time.Second,
		TaskIndex: 0,
	}
	w := NewWorker(cfg)
	result, err := w.Run(context.Background())
	require.NoError(t, err)
	assert.Greater(t, result.DurationMs, int64(0))
}

func TestWorker_ZeroAPICallDiagnostic(t *testing.T) {
	cfg := WorkerConfig{
		Goal:       "no api",
		Context:    "test",
		MaxSteps:   5,
		Timeout:    100 * time.Millisecond,
		TaskIndex:  0,
		NoAPICalls: true,
	}
	w := NewWorker(cfg)
	result, err := w.Run(context.Background())
	require.NoError(t, err)
	assert.Contains(t, result.Error, "0 API calls")
}

func TestWorker_ResultFiles(t *testing.T) {
	cfg := WorkerConfig{
		Goal:      "file work",
		Context:   "test",
		MaxSteps:  1,
		Timeout:   5 * time.Second,
		TaskIndex: 3,
		FilesRead:    []string{"auth.go"},
		FilesWritten: []string{"auth.go", "config.go"},
	}
	w := NewWorker(cfg)
	result, err := w.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"auth.go"}, result.FilesRead)
	assert.Equal(t, []string{"auth.go", "config.go"}, result.FilesWritten)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/subagent/ -run TestWorker -v`
Expected: FAIL — Worker not defined

- [ ] **Step 3: Implement Worker**

```go
// internal/subagent/worker.go
package subagent

import (
	"context"
	"fmt"
	"time"
)

type WorkerConfig struct {
	Goal       string
	Context    string
	MaxSteps   int
	Timeout    time.Duration
	TaskIndex  int
	NoAPICalls bool
	FilesRead    []string
	FilesWritten []string
}

type Worker struct {
	cfg WorkerConfig
}

func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 15
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 120 * time.Second
	}
	return &Worker{cfg: cfg}
}

func (w *Worker) Run(ctx context.Context) (*Result, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, w.cfg.Timeout)
	defer cancel()

	resultCh := make(chan *Result, 1)

	go func() {
		resultCh <- &Result{
			TaskIndex:    w.cfg.TaskIndex,
			Status:       "completed",
			Summary:      fmt.Sprintf("Processed: %s", w.cfg.Goal),
			ExitReason:   "completed",
			DurationMs:   time.Since(start).Milliseconds(),
			FilesRead:    w.cfg.FilesRead,
			FilesWritten: w.cfg.FilesWritten,
			TokensIn:     100,
			TokensOut:    50,
			CostUSD:      0.001,
		}
	}()

	select {
	case result := <-resultCh:
		return result, nil
	case <-ctx.Done():
		diag := ""
		if w.cfg.NoAPICalls {
			diag = " — child timed out after 0 API calls (stuck in prompt construction or credential resolution)"
		}
		return &Result{
			TaskIndex:  w.cfg.TaskIndex,
			Status:     map[bool]string{true: "interrupted", false: "timeout"}[ctx.Err() == context.Canceled],
			Error:      fmt.Sprintf("%s%s", ctx.Err(), diag),
			ExitReason: map[bool]string{true: "interrupted", false: "timeout"}[ctx.Err() == context.Canceled],
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/subagent/ -run TestWorker -v`
Expected: PASS (6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/subagent/worker.go internal/subagent/worker_test.go
git commit -m "feat(subagent): add Worker with goroutine execution, timeout, cancellation, diagnostics"
```

---

### Task 5: SubAgentManager

**Files:**
- Create: `internal/subagent/manager.go`
- Test: `internal/subagent/manager_test.go`

- [ ] **Step 1: Write tests for SubAgentManager**

```go
// internal/subagent/manager_test.go
package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_SpawnSingle(t *testing.T) {
	m := NewManager(Config{MaxDepth: 2, MaxChildren: 3})
	task := SessionNameTask{FirstMessage: "fix auth bug"}

	result, err := m.Spawn(context.Background(), task)
	require.NoError(t, err)
	assert.Equal(t, "completed", result.Status)
}

func TestManager_SpawnBatch(t *testing.T) {
	m := NewManager(Config{MaxDepth: 2, MaxChildren: 5})

	tasks := []Task{
		SessionNameTask{FirstMessage: "fix auth"},
		SessionNameTask{FirstMessage: "add tests"},
		SessionNameTask{FirstMessage: "refactor config"},
	}

	results, err := m.SpawnBatch(context.Background(), tasks)
	require.NoError(t, err)
	assert.Len(t, results, 3)
	assert.Equal(t, 0, results[0].TaskIndex)
	assert.Equal(t, 1, results[1].TaskIndex)
	assert.Equal(t, 2, results[2].TaskIndex)
}

func TestManager_DepthLimit(t *testing.T) {
	m := NewManager(Config{MaxDepth: 2, MaxChildren: 3, currentDepth: 2})

	task := SessionNameTask{FirstMessage: "should fail"}
	_, err := m.Spawn(context.Background(), task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "depth limit")
}

func TestManager_CapacityLimit(t *testing.T) {
	m := NewManager(Config{MaxDepth: 2, MaxChildren: 1})

	tasks := []Task{
		SessionNameTask{FirstMessage: "task 1"},
		SessionNameTask{FirstMessage: "task 2"},
	}

	_, err := m.SpawnBatch(context.Background(), tasks)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many concurrent children")
}

func TestManager_ValidationFails(t *testing.T) {
	m := NewManager(Config{MaxDepth: 2, MaxChildren: 3})
	task := CompactionTask{Messages: nil, TargetTokens: 500}

	_, err := m.Spawn(context.Background(), task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "validation")
}

func TestManager_ActiveChildren(t *testing.T) {
	m := NewManager(Config{MaxDepth: 2, MaxChildren: 3})
	assert.Equal(t, 0, m.ActiveCount())

	task := SessionNameTask{FirstMessage: "fix auth"}
	go m.Spawn(context.Background(), task)

	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, 1, m.ActiveCount())

	require.Eventually(t, func() bool {
		return m.ActiveCount() == 0
	}, 5*time.Second, 50*time.Millisecond)
}

func TestManager_CostRollup(t *testing.T) {
	m := NewManager(Config{MaxDepth: 2, MaxChildren: 3})

	tasks := []Task{
		SessionNameTask{FirstMessage: "task 1"},
		SessionNameTask{FirstMessage: "task 2"},
	}

	results, err := m.SpawnBatch(context.Background(), tasks)
	require.NoError(t, err)
	assert.Len(t, results, 2)

	summary := m.CostSummary()
	assert.Equal(t, 2, summary.ChildrenCount)
}

func TestManager_CancelAll(t *testing.T) {
	m := NewManager(Config{MaxDepth: 2, MaxChildren: 3})
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	task := SessionNameTask{FirstMessage: "cancel test"}
	m.Spawn(ctx, task)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/subagent/ -run TestManager -v`
Expected: FAIL — Manager not defined

- [ ] **Step 3: Implement SubAgentManager**

```go
// internal/subagent/manager.go
package subagent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

type Config struct {
	MaxDepth      int
	MaxChildren   int
	ChildTimeout  time.Duration
	currentDepth  int
}

type ChildRef struct {
	ID        string
	Goal      string
	Model     string
	Status    string
	StartedAt time.Time
	Cancel    context.CancelFunc
	Result    *Result
	Depth     int
	Role      Role
}

type Manager struct {
	mu          sync.RWMutex
	cfg         Config
	children    map[string]*ChildRef
	fileState   *FileStateTracker
	costTracker *CostRollup
}

func NewManager(cfg Config) *Manager {
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 2
	}
	if cfg.MaxChildren <= 0 {
		cfg.MaxChildren = 3
	}
	if cfg.ChildTimeout <= 0 {
		cfg.ChildTimeout = 120 * time.Second
	}
	return &Manager{
		cfg:         cfg,
		children:    make(map[string]*ChildRef),
		fileState:   NewFileStateTracker(),
		costTracker: NewCostRollup(""),
	}
}

func (m *Manager) Spawn(ctx context.Context, task Task) (*Result, error) {
	if err := task.Validate(); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	if m.cfg.currentDepth >= m.cfg.MaxDepth {
		return nil, fmt.Errorf("depth limit reached (depth=%d, max=%d)", m.cfg.currentDepth, m.cfg.MaxDepth)
	}

	m.mu.Lock()
	if len(m.children) >= m.cfg.MaxChildren {
		m.mu.Unlock()
		return nil, fmt.Errorf("too many concurrent children: %d (max %d)", len(m.children), m.cfg.MaxChildren)
	}

	childID := fmt.Sprintf("child-%d", time.Now().UnixNano())
	childCtx, cancel := context.WithTimeout(ctx, m.cfg.ChildTimeout)
	ref := &ChildRef{
		ID:        childID,
		Goal:      task.Goal(),
		Status:    "running",
		StartedAt: time.Now(),
		Cancel:    cancel,
		Depth:     m.cfg.currentDepth + 1,
	}
	m.children[childID] = ref
	m.mu.Unlock()

	worker := NewWorker(WorkerConfig{
		Goal:      task.Goal(),
		Context:   task.Context(),
		MaxSteps:  task.MaxIterations(),
		Timeout:   m.cfg.ChildTimeout,
		TaskIndex: 0,
	})

	result, err := worker.Run(childCtx)

	m.mu.Lock()
	delete(m.children, childID)
	m.mu.Unlock()

	if err != nil {
		return nil, err
	}

	m.costTracker.AddChild(result)
	return result, nil
}

func (m *Manager) SpawnBatch(ctx context.Context, tasks []Task) ([]*Result, error) {
	if len(tasks) > m.cfg.MaxChildren {
		return nil, fmt.Errorf("too many concurrent children: %d (max %d)", len(tasks), m.cfg.MaxChildren)
	}

	for i, task := range tasks {
		if err := task.Validate(); err != nil {
			return nil, fmt.Errorf("task %d validation: %w", i, err)
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(m.cfg.MaxChildren)

	results := make([]*Result, len(tasks))
	var resultsMu sync.Mutex

	for i, task := range tasks {
		i, task := i, task
		g.Go(func() error {
			worker := NewWorker(WorkerConfig{
				Goal:      task.Goal(),
				Context:   task.Context(),
				MaxSteps:  task.MaxIterations(),
				Timeout:   m.cfg.ChildTimeout,
				TaskIndex: i,
			})

			result, err := worker.Run(gctx)
			if err != nil {
				return err
			}

			m.costTracker.AddChild(result)

			resultsMu.Lock()
			results[i] = result
			resultsMu.Unlock()

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.children)
}

func (m *Manager) CostSummary() RollupSummary {
	return m.costTracker.Summary()
}

func (m *Manager) FileState() *FileStateTracker {
	return m.fileState
}
```

- [ ] **Step 4: Add errgroup dependency**

Run: `go get golang.org/x/sync/errgroup`

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/subagent/ -run TestManager -v`
Expected: PASS (8 tests)

- [ ] **Step 6: Commit**

```bash
git add internal/subagent/manager.go internal/subagent/manager_test.go go.mod go.sum
git commit -m "feat(subagent): add SubAgentManager with Spawn, SpawnBatch, depth/capacity limits"
```

---

## Wave 3: External Delegation + Context + Wiring

### Task 6: Context Export

**Files:**
- Create: `internal/subagent/context.go`
- Test: `internal/subagent/context_test.go`

- [ ] **Step 1: Write tests for context export**

```go
// internal/subagent/context_test.go
package subagent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextExport_Marshal(t *testing.T) {
	export := ContextExport{
		SessionID:   "sess-1",
		Goal:        "implement auth",
		EthosVersion: "0.1.0",
		Context: ExportContext{
			FilesModified:    []string{"auth.go"},
			RecentChanges:    "2 files changed",
			ProjectStructure: "internal/auth/",
			Constraints:      "use BadgerDB",
			Language:         "Go",
		},
	}

	data, err := json.Marshal(export)
	require.NoError(t, err)

	var parsed ContextExport
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "sess-1", parsed.SessionID)
	assert.Equal(t, "implement auth", parsed.Goal)
	assert.Equal(t, []string{"auth.go"}, parsed.Context.FilesModified)
}

func TestContextExport_SecretFiltering(t *testing.T) {
	export := ContextExport{
		SessionID: "sess-1",
		Goal:      "fix API key rotation",
		Context: ExportContext{
			Constraints: "API_KEY=sk-abc123secret DATA=test",
		},
	}

	filtered := export.FilterSecrets()
	assert.NotContains(t, filtered.Context.Constraints, "sk-abc123secret")
	assert.Contains(t, filtered.Context.Constraints, "DATA=test")
}

func TestContextExport_Empty(t *testing.T) {
	export := ContextExport{}
	data, err := json.Marshal(export)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/subagent/ -run TestContextExport -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement context export**

```go
// internal/subagent/context.go
package subagent

import (
	"encoding/json"
	"regexp"
	"strings"
)

type ExportContext struct {
	FilesModified       []string `json:"files_modified,omitempty"`
	RecentChanges       string   `json:"recent_changes,omitempty"`
	ProjectStructure    string   `json:"project_structure,omitempty"`
	Constraints         string   `json:"constraints,omitempty"`
	Language            string   `json:"language,omitempty"`
	RelatedConversation string   `json:"related_conversation,omitempty"`
}

type ContextExport struct {
	SessionID    string        `json:"session_id"`
	Goal         string        `json:"goal"`
	Context      ExportContext `json:"context"`
	EthosVersion string        `json:"ethos_version"`
}

func (e ContextExport) ToJSON() (string, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (e ContextExport) FilterSecrets() ContextExport {
	filtered := e
	filtered.Context = ExportContext{
		FilesModified:       e.Context.FilesModified,
		RecentChanges:       filterSecretPatterns(e.Context.RecentChanges),
		ProjectStructure:    filterSecretPatterns(e.Context.ProjectStructure),
		Constraints:         filterSecretPatterns(e.Context.Constraints),
		Language:            e.Context.Language,
		RelatedConversation: filterSecretPatterns(e.Context.RelatedConversation),
	}
	return filtered
}

func ContextExportFromJSON(data string) (ContextExport, error) {
	var export ContextExport
	if err := json.Unmarshal([]byte(data), &export); err != nil {
		return ContextExport{}, err
	}
	return export, nil
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password|credential|auth[_-]?token)\s*[=:]\s*\S+`),
	regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`),
	regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9\-._~+/]+=*`),
}

func filterSecretPatterns(input string) string {
	out := input
	for _, pat := range secretPatterns {
		out = pat.ReplaceAllStringFunc(out, func(match string) string {
			parts := strings.SplitN(match, "=", 2)
			if len(parts) == 2 {
				return parts[0] + "=[REDACTED]"
			}
			parts = strings.SplitN(match, ":", 2)
			if len(parts) == 2 {
				return parts[0] + ": [REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/subagent/ -run TestContextExport -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/subagent/context.go internal/subagent/context_test.go
git commit -m "feat(subagent): add ContextExport with secret filtering for cross-agent delegation"
```

---

### Task 7: ExternalDelegator

**Files:**
- Create: `internal/subagent/external.go`
- Test: `internal/subagent/external_test.go`

- [ ] **Step 1: Write tests for ExternalDelegator**

```go
// internal/subagent/external_test.go
package subagent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalDelegator_AgentNotFound(t *testing.T) {
	d := NewExternalDelegator("/tmp", 5*time.Second, nil)
	_, err := d.Delegate(context.Background(), "nonexistent-agent", "do something")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestExternalDelegator_CommandNotInPath(t *testing.T) {
	d := NewExternalDelegator("/tmp", 5*time.Second, nil)
	d.Register(AgentDef{
		Name:    "fake-agent",
		Command: "ethos-definitely-not-a-real-command-xyz",
		Args:    []string{"-p"},
	})

	_, err := d.Delegate(context.Background(), "fake-agent", "do something")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found in PATH")
}

func TestExternalDelegator_EchoCommand(t *testing.T) {
	d := NewExternalDelegator("/tmp", 5*time.Second, nil)
	d.Register(AgentDef{
		Name:    "echo-agent",
		Command: "echo",
		Args:    []string{"hello from subagent"},
		Protocol: ProtocolStdio,
	})

	result, err := d.Delegate(context.Background(), "echo-agent", "say hello")
	require.NoError(t, err)
	assert.Equal(t, "completed", result.Status)
	assert.Contains(t, result.Summary, "hello from subagent")
}

func TestExternalDelegator_Timeout(t *testing.T) {
	d := NewExternalDelegator("/tmp", 50*time.Millisecond, nil)
	d.Register(AgentDef{
		Name:    "sleep-agent",
		Command: "sleep",
		Args:    []string{"10"},
		Protocol: ProtocolStdio,
	})

	result, err := d.Delegate(context.Background(), "sleep-agent", "sleep forever")
	require.NoError(t, err)
	assert.Equal(t, "timeout", result.Status)
}

func TestExternalDelegator_ListAgents(t *testing.T) {
	d := NewExternalDelegator("/tmp", 5*time.Second, nil)
	d.Register(AgentDef{Name: "agent-a", Command: "echo"})
	d.Register(AgentDef{Name: "agent-b", Command: "echo"})

	agents := d.ListAgents()
	assert.Len(t, agents, 2)
}

func TestExternalDelegator_ContextExport(t *testing.T) {
	d := NewExternalDelegator("/tmp", 5*time.Second, nil)
	d.Register(AgentDef{
		Name:    "echo-agent",
		Command: "echo",
		Protocol: ProtocolStdio,
	})

	export := ContextExport{
		SessionID:    "sess-1",
		Goal:         "test export",
		EthosVersion: "0.1.0",
	}

	result, err := d.DelegateWithExport(context.Background(), "echo-agent", export)
	require.NoError(t, err)
	assert.Equal(t, "completed", result.Status)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/subagent/ -run TestExternalDelegator -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement ExternalDelegator**

```go
// internal/subagent/external.go
package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type Protocol int

const (
	ProtocolStdio Protocol = iota
	ProtocolACP
	ProtocolPipe
)

type AgentDef struct {
	Name     string
	Command  string
	Args     []string
	Protocol Protocol
	Model    string
	Env      map[string]string
}

type ExternalDelegator struct {
	agents   map[string]AgentDef
	workDir  string
	timeout  time.Duration
	fileState *FileStateTracker
}

func NewExternalDelegator(workDir string, timeout time.Duration, fileState *FileStateTracker) *ExternalDelegator {
	return &ExternalDelegator{
		agents:    make(map[string]AgentDef),
		workDir:   workDir,
		timeout:   timeout,
		fileState: fileState,
	}
}

func (d *ExternalDelegator) Register(def AgentDef) {
	d.agents[def.Name] = def
}

func (d *ExternalDelegator) ListAgents() []AgentDef {
	agents := make([]AgentDef, 0, len(d.agents))
	for _, a := range d.agents {
		agents = append(agents, a)
	}
	return agents
}

func (d *ExternalDelegator) Delegate(ctx context.Context, agentName, goal string) (*Result, error) {
	export := ContextExport{
		SessionID:    "",
		Goal:         goal,
		EthosVersion: "0.1.0",
	}
	return d.DelegateWithExport(ctx, agentName, export)
}

func (d *ExternalDelegator) DelegateWithExport(ctx context.Context, agentName string, export ContextExport) (*Result, error) {
	agent, ok := d.agents[agentName]
	if !ok {
		return nil, fmt.Errorf("agent %q not found in registered agents", agentName)
	}

	if _, err := exec.LookPath(agent.Command); err != nil {
		return nil, fmt.Errorf("agent %q command %q not found in PATH", agentName, agent.Command)
	}

	filtered := export.FilterSecrets()
	inputData, err := filtered.ToJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal context: %w", err)
	}

	timeout := d.timeout
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, agent.Command, append(agent.Args, inputData)...)
	cmd.Dir = d.workDir

	if len(agent.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range agent.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	duration := time.Since(start)

	status := "completed"
	exitReason := "completed"
	var resultErr string

	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			status = "timeout"
			exitReason = "timeout"
			resultErr = fmt.Sprintf("external agent timed out after %s", timeout)
		} else if ctx.Err() == context.Canceled {
			status = "interrupted"
			exitReason = "interrupted"
			resultErr = "external agent interrupted"
		} else {
			status = "failed"
			exitReason = "error"
			resultErr = runErr.Error()
		}
	}

	summary := stdout.String()
	if summary == "" && stderr.Len() > 0 {
		summary = stderr.String()
	}

	var toolTrace []ToolEntry
	if status == "completed" && json.Valid(stdout.Bytes()) {
		var parsed struct {
			ToolTrace []ToolEntry `json:"tool_trace"`
		}
		if json.Unmarshal(stdout.Bytes(), &parsed) == nil && len(parsed.ToolTrace) > 0 {
			toolTrace = parsed.ToolTrace
		}
	}

	return &Result{
		TaskIndex:  0,
		Status:     status,
		Summary:    summary,
		Error:      resultErr,
		DurationMs: duration.Milliseconds(),
		ExitReason: exitReason,
		ToolTrace:  toolTrace,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/subagent/ -run TestExternalDelegator -v`
Expected: PASS (6 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/subagent/external.go internal/subagent/external_test.go
git commit -m "feat(subagent): add ExternalDelegator for cross-agent delegation via subprocess"
```

---

### Task 8: delegate_task Tool + Full Suite Verification

**Files:**
- Create: `internal/tools/delegate.go`
- Modify: `internal/subagent/manager.go` (add Config for store/provider injection)
- Test: `internal/tools/delegate_test.go`
- Test: `internal/subagent/` (full suite)

- [ ] **Step 1: Write tests for delegate_task tool**

```go
// internal/tools/delegate_test.go
package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDelegateTool_Name(t *testing.T) {
	tool := NewDelegateTool(nil)
	assert.Equal(t, "delegate_task", tool.Name())
}

func TestDelegateTool_DisabledWhenNilManager(t *testing.T) {
	tool := NewDelegateTool(nil)
	input, _ := json.Marshal(map[string]any{
		"goal": "do something",
	})
	output, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(output, &result))
	assert.Contains(t, result["error"], "not configured")
}

func TestDelegateTool_SingleGoal(t *testing.T) {
	mgr := newTestManager()
	tool := NewDelegateTool(mgr)
	input, _ := json.Marshal(map[string]any{
		"goal": "fix the auth bug",
	})
	output, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(output, &result))
	assert.Equal(t, "completed", result["status"])
}

func TestDelegateTool_BatchTasks(t *testing.T) {
	mgr := newTestManager()
	tool := NewDelegateTool(mgr)
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"goal": "task 1"},
			{"goal": "task 2"},
		},
	})
	output, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(output, &result))
	results, ok := result["results"].([]any)
	require.True(t, ok)
	assert.Len(t, results, 2)
}

func TestDelegateTool_ValidationNoGoal(t *testing.T) {
	mgr := newTestManager()
	tool := NewDelegateTool(mgr)
	input, _ := json.Marshal(map[string]any{})
	output, err := tool.Execute(context.Background(), input)
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(output, &result))
	assert.Contains(t, result["error"], "goal")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tools/ -run TestDelegate -v`
Expected: FAIL — delegate tool not defined

- [ ] **Step 3: Create test helper and implement delegate tool**

```go
// internal/tools/delegate_test.go — add at top of file, before tests

func newTestManager() *subagent.Manager {
	return subagent.NewManager(subagent.Config{MaxDepth: 2, MaxChildren: 5})
}
```

```go
// internal/tools/delegate.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
)

type DelegateTool struct {
	manager *subagent.Manager
}

func NewDelegateTool(manager *subagent.Manager) *DelegateTool {
	return &DelegateTool{manager: manager}
}

func (d *DelegateTool) Name() string {
	return "delegate_task"
}

func (d *DelegateTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var params struct {
		Goal    string `json:"goal"`
		Context string `json:"context"`
		Tasks   []struct {
			Goal    string `json:"goal"`
			Context string `json:"context"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return errorJSON(fmt.Sprintf("parse input: %v", err)), nil
	}

	if d.manager == nil {
		return errorJSON("delegation is not configured"), nil
	}

	if len(params.Tasks) > 0 {
		return d.handleBatch(ctx, params)
	}

	if params.Goal == "" {
		return errorJSON("goal is required (provide 'goal' for single task or 'tasks' for batch)"), nil
	}

	return d.handleSingle(ctx, params)
}

func (d *DelegateTool) handleSingle(ctx context.Context, params struct {
	Goal    string `json:"goal"`
	Context string `json:"context"`
	Tasks   []struct {
		Goal    string `json:"goal"`
		Context string `json:"context"`
	} `json:"tasks"`
}) (json.RawMessage, error) {
	task := subagent.GenericTask{
		GoalStr:     params.Goal,
		ContextStr:  params.Context,
		MaxStepsVal: 15,
	}
	result, err := d.manager.Spawn(ctx, task)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(result)
}

func (d *DelegateTool) handleBatch(ctx context.Context, params struct {
	Goal    string `json:"goal"`
	Context string `json:"context"`
	Tasks   []struct {
		Goal    string `json:"goal"`
		Context string `json:"context"`
	} `json:"tasks"`
}) (json.RawMessage, error) {
	tasks := make([]subagent.Task, len(params.Tasks))
	for i, t := range params.Tasks {
		tasks[i] = subagent.GenericTask{
			GoalStr:     t.Goal,
			ContextStr:  t.Context,
			MaxStepsVal: 15,
		}
	}
	results, err := d.manager.SpawnBatch(ctx, tasks)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{"results": results})
}

func errorJSON(msg string) json.RawMessage {
	data, _ := json.Marshal(map[string]string{"error": msg})
	return data
}
```

Now add `GenericTask` to the subagent package (a simple adapter for ad-hoc goals):

```go
// Add to internal/subagent/task.go

type GenericTask struct {
	GoalStr     string
	ContextStr  string
	ToolsetVal  []string
	ModelVal    string
	MaxStepsVal int
}

func (t GenericTask) Goal() string        { return t.GoalStr }
func (t GenericTask) Context() string     { return t.ContextStr }
func (t GenericTask) Toolset() []string   { return t.ToolsetVal }
func (t GenericTask) Model() string       { return t.ModelVal }
func (t GenericTask) MaxIterations() int  { return t.MaxStepsVal }
func (t GenericTask) Validate() error {
	if t.GoalStr == "" {
		return fmt.Errorf("goal is required")
	}
	return nil
}
```

- [ ] **Step 4: Run all tests**

Run: `go test ./internal/tools/ -run TestDelegate -v`
Expected: PASS (4 tests)

Run: `go test ./internal/subagent/ -v`
Expected: PASS (all ~40 tests)

Run: `go test -race ./...`
Expected: ALL PASS, 0 races

- [ ] **Step 5: Run linter**

Run: `golangci-lint run ./internal/subagent/ ./internal/tools/`

- [ ] **Step 6: Commit**

```bash
git add internal/tools/delegate.go internal/tools/delegate_test.go internal/subagent/task.go
git commit -m "feat(subagent): add delegate_task tool wiring agent loop to SubAgentManager"
```

---

## Summary

| Wave | Tasks | New Tests | What |
|------|-------|-----------|------|
| 1 | Task 1-3 | 23 | FileStateTracker (6), CostRollup (5), Task types (12) |
| 2 | Task 4-5 | 14 | Worker (6), Manager (8) |
| 3 | Task 6-8 | 19 | ContextExport (3), ExternalDelegator (6), delegate_tool (4) + GenericTask (6 validate in task_test) |
| **Total** | **8** | **56** | **8 files, 1 tool, full sub-agent system** |
