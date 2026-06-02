package subagent

// B141: This package imports internal/personality and internal/providers.
// Be aware of import cycle risk — personality and providers must NOT
// import subagent. The current dependency direction is safe (subagent
// depends on them) but any future change that makes personality or
// providers depend on subagent will create a cycle that breaks the build.
// If that happens, refactor the shared types into a separate package
// (e.g. internal/subagent/types).

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// Role determines whether a sub-agent is a worker or an orchestrator.
type Role int

const (
	RoleWorker Role = iota
	RoleOrchestrator
)

// Task is the interface every sub-agent task must implement.
type Task interface {
	Goal() string
	Context() string
	Toolset() []string
	Model() string
	MaxIterations() int
	Validate() error
}

// ToolEntry records a single tool invocation during task execution.
type ToolEntry struct {
	Tool    string `json:"tool"`
	ArgsLen int    `json:"args_len"`
}

// Result captures the outcome of a single sub-agent task execution.
// This is the authoritative definition — cost.go references it.
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

// ---------------------------------------------------------------------------
// Built-in task types
// ---------------------------------------------------------------------------

// CompactionTask summarizes conversation history into a concise summary.
type CompactionTask struct {
	Messages      []providers.Message
	TargetTokens  int
	OverrideModel string
}

func (t *CompactionTask) Goal() string {
	return fmt.Sprintf(
		"Summarize the following conversation history into a concise summary preserving all decisions, code patterns, and important context. Target approximately %d tokens.",
		t.TargetTokens,
	)
}

func (t *CompactionTask) Context() string {
	data, err := json.Marshal(t.Messages)
	if err != nil {
		return ""
	}
	return string(data)
}

func (t *CompactionTask) Toolset() []string  { return nil }
func (t *CompactionTask) Model() string      { return t.OverrideModel }
func (t *CompactionTask) MaxIterations() int { return 15 }

func (t *CompactionTask) Validate() error {
	if len(t.Messages) == 0 {
		return errors.New("compaction task: messages must not be empty")
	}
	if t.TargetTokens < 100 {
		return errors.New("compaction task: target tokens must be at least 100")
	}
	return nil
}

// SessionNameTask generates a concise session title from the first message.
type SessionNameTask struct {
	FirstMessage string
	MaxTokens    int
}

func (t *SessionNameTask) Goal() string {
	return "Generate a concise session title (max 10 words) that captures the main topic of this conversation."
}

func (t *SessionNameTask) Context() string    { return t.FirstMessage }
func (t *SessionNameTask) Toolset() []string  { return nil }
func (t *SessionNameTask) Model() string      { return "" }
func (t *SessionNameTask) MaxIterations() int { return 5 }

func (t *SessionNameTask) Validate() error {
	if t.FirstMessage == "" {
		return errors.New("session name task: first message must not be empty")
	}
	return nil
}

// PersonalityUpdateTask analyzes conversation for relationship milestones.
type PersonalityUpdateTask struct {
	SessionMessages []providers.Message
	CurrentLevel    personality.Level
	AgentName       string
}

func (t *PersonalityUpdateTask) Goal() string {
	return "Analyze this conversation for relationship milestones, emotional beats, and interaction patterns. Return JSON: {beats: [], mood: string, suggestions: []}"
}

func (t *PersonalityUpdateTask) Context() string {
	obj := map[string]any{
		"messages":      t.SessionMessages,
		"current_level": t.CurrentLevel,
		"agent_name":    t.AgentName,
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	return string(data)
}

func (t *PersonalityUpdateTask) Toolset() []string  { return nil }
func (t *PersonalityUpdateTask) Model() string      { return "" }
func (t *PersonalityUpdateTask) MaxIterations() int { return 10 }

func (t *PersonalityUpdateTask) Validate() error {
	if len(t.SessionMessages) == 0 {
		return errors.New("personality update task: session messages must not be empty")
	}
	return nil
}

// VisualDebugTask compares two screenshots and reports visual differences.
type VisualDebugTask struct {
	ScreenshotPath string
	ExpectedPath   string
}

func (t *VisualDebugTask) Goal() string {
	return "Compare two screenshots and report visual differences."
}

func (t *VisualDebugTask) Context() string {
	return fmt.Sprintf("screenshot: %s, expected: %s", t.ScreenshotPath, t.ExpectedPath)
}

func (t *VisualDebugTask) Toolset() []string  { return []string{"fs"} }
func (t *VisualDebugTask) Model() string      { return "" }
func (t *VisualDebugTask) MaxIterations() int { return 5 }

func (t *VisualDebugTask) Validate() error {
	if t.ScreenshotPath == "" {
		return errors.New("visual debug task: screenshot path must not be empty")
	}
	return nil
}

// GenericTask is a general-purpose task that satisfies the Task interface.
// It is used by the delegate_task tool to spawn arbitrary sub-agent work.
type GenericTask struct {
	GoalStr     string
	ContextStr  string
	ToolsetVal  []string
	ModelVal    string
	MaxStepsVal int
}

func (t GenericTask) Goal() string       { return t.GoalStr }
func (t GenericTask) Context() string    { return t.ContextStr }
func (t GenericTask) Toolset() []string  { return t.ToolsetVal }
func (t GenericTask) Model() string      { return t.ModelVal }
func (t GenericTask) MaxIterations() int { return t.MaxStepsVal }
func (t GenericTask) Validate() error {
	if t.GoalStr == "" {
		return fmt.Errorf("goal is required")
	}
	return nil
}
