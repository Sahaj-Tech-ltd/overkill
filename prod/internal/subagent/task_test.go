package subagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// --- CompactionTask ---

func TestCompactionTask_Validate(t *testing.T) {
	// Empty messages → error
	task := &CompactionTask{Messages: nil, TargetTokens: 500}
	if err := task.Validate(); err == nil {
		t.Error("expected error for empty messages")
	}

	// TargetTokens < 100 → error
	task = &CompactionTask{
		Messages:     []providers.Message{{Role: "user", Content: "hello"}},
		TargetTokens: 50,
	}
	if err := task.Validate(); err == nil {
		t.Error("expected error for TargetTokens < 100")
	}

	// Valid → no error
	task = &CompactionTask{
		Messages:     []providers.Message{{Role: "user", Content: "hello"}},
		TargetTokens: 500,
	}
	if err := task.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCompactionTask_Fields(t *testing.T) {
	task := &CompactionTask{
		Messages:      []providers.Message{{Role: "user", Content: "test"}},
		TargetTokens:  500,
		OverrideModel: "gpt-4o-mini",
	}

	if !strings.Contains(strings.ToLower(task.Goal()), "summarize") {
		t.Error("Goal should contain 'summarize'")
	}
	if task.Context() == "" {
		t.Error("Context should not be empty")
	}
	if task.Toolset() != nil {
		t.Error("Toolset should be nil")
	}
	if task.Model() != "gpt-4o-mini" {
		t.Errorf("Model = %q, want %q", task.Model(), "gpt-4o-mini")
	}
	if task.MaxIterations() != 15 {
		t.Errorf("MaxIterations = %d, want 15", task.MaxIterations())
	}
}

// --- SessionNameTask ---

func TestSessionNameTask_Validate(t *testing.T) {
	// Empty → error
	task := &SessionNameTask{FirstMessage: ""}
	if err := task.Validate(); err == nil {
		t.Error("expected error for empty FirstMessage")
	}

	// Non-empty → no error
	task = &SessionNameTask{FirstMessage: "Help me refactor auth"}
	if err := task.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSessionNameTask_Fields(t *testing.T) {
	task := &SessionNameTask{FirstMessage: "Help me refactor the auth module"}

	if !strings.Contains(strings.ToLower(task.Goal()), "session title") {
		t.Error("Goal should contain 'session title'")
	}
	if !strings.Contains(task.Context(), "Help me refactor the auth module") {
		t.Error("Context should contain FirstMessage")
	}
	if task.Toolset() != nil {
		t.Error("Toolset should be nil")
	}
	if task.Model() != "" {
		t.Errorf("Model = %q, want empty", task.Model())
	}
	if task.MaxIterations() != 5 {
		t.Errorf("MaxIterations = %d, want 5", task.MaxIterations())
	}
}

// --- PersonalityUpdateTask ---

func TestPersonalityUpdateTask_Validate(t *testing.T) {
	// Empty → error
	task := &PersonalityUpdateTask{SessionMessages: nil}
	if err := task.Validate(); err == nil {
		t.Error("expected error for empty SessionMessages")
	}

	// Non-empty → no error
	task = &PersonalityUpdateTask{
		SessionMessages: []providers.Message{{Role: "user", Content: "hi"}},
		CurrentLevel:    personality.LevelFull,
		AgentName:       "overkill",
	}
	if err := task.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPersonalityUpdateTask_Fields(t *testing.T) {
	task := &PersonalityUpdateTask{
		SessionMessages: []providers.Message{{Role: "user", Content: "hello"}},
		CurrentLevel:    personality.LevelWitty,
		AgentName:       "overkill",
	}

	if !strings.Contains(strings.ToLower(task.Goal()), "relationship") {
		t.Error("Goal should contain 'relationship'")
	}

	ctx := task.Context()
	if ctx == "" {
		t.Error("Context should not be empty")
	}

	// Verify context is valid JSON and contains expected fields
	var parsed map[string]any
	if err := json.Unmarshal([]byte(ctx), &parsed); err != nil {
		t.Errorf("Context should be valid JSON: %v", err)
	}
	if _, ok := parsed["messages"]; !ok {
		t.Error("Context JSON should contain 'messages'")
	}
	if _, ok := parsed["current_level"]; !ok {
		t.Error("Context JSON should contain 'current_level'")
	}
	if _, ok := parsed["agent_name"]; !ok {
		t.Error("Context JSON should contain 'agent_name'")
	}

	if task.Toolset() != nil {
		t.Error("Toolset should be nil")
	}
	if task.Model() != "" {
		t.Errorf("Model = %q, want empty", task.Model())
	}
	if task.MaxIterations() != 10 {
		t.Errorf("MaxIterations = %d, want 10", task.MaxIterations())
	}
}

// --- VisualDebugTask ---

func TestVisualDebugTask_Validate(t *testing.T) {
	// Empty path → error
	task := &VisualDebugTask{ScreenshotPath: "", ExpectedPath: "/tmp/expected.png"}
	if err := task.Validate(); err == nil {
		t.Error("expected error for empty ScreenshotPath")
	}

	// Valid → no error
	task = &VisualDebugTask{ScreenshotPath: "/tmp/shot.png", ExpectedPath: "/tmp/expected.png"}
	if err := task.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVisualDebugTask_StubError(t *testing.T) {
	task := &VisualDebugTask{
		ScreenshotPath: "/tmp/shot.png",
		ExpectedPath:   "/tmp/expected.png",
	}

	if !strings.Contains(strings.ToLower(task.Goal()), "visual") {
		t.Error("Goal should contain 'visual'")
	}

	toolset := task.Toolset()
	if len(toolset) != 1 || toolset[0] != "fs" {
		t.Errorf("Toolset = %v, want [\"fs\"]", toolset)
	}

	if !strings.Contains(task.Context(), "/tmp/shot.png") {
		t.Error("Context should contain screenshot path")
	}
	if !strings.Contains(task.Context(), "/tmp/expected.png") {
		t.Error("Context should contain expected path")
	}
	if task.Model() != "" {
		t.Errorf("Model = %q, want empty", task.Model())
	}
	if task.MaxIterations() != 5 {
		t.Errorf("MaxIterations = %d, want 5", task.MaxIterations())
	}
}
