package agent

import (
	"context"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// fakeReflector implements Reflector for testing.
type fakeReflector struct {
	shouldReflect bool
	rootCause     string
	hypothesis    string
	confidence    float64
	mode          string
}

func (f *fakeReflector) IsFailure(_, _, _ string) bool { return f.shouldReflect }
func (f *fakeReflector) Reflect(_ Failure) Reflection {
	return Reflection{
		Mode:       f.mode,
		RootCause:  f.rootCause,
		Hypothesis: f.hypothesis,
		Confidence: f.confidence,
	}
}
func (f *fakeReflector) FormatNote(_ string, r Reflection) string {
	return r.Hypothesis
}

// fakeLearningRecorder implements LearningRecorder for testing.
type fakeLearningRecorder struct {
	recorded []string
}

func (f *fakeLearningRecorder) RecordSuccess(class string) bool {
	f.recorded = append(f.recorded, class)
	return true
}

// fakeRunner is a simple runner for RunPhase tests.
type fakeRunner struct {
	results []string // canned responses per call
	callIdx int
}

func (f *fakeRunner) run(_ context.Context, _, _ string) ([]providers.Message, error) {
	content := "default"
	if f.callIdx < len(f.results) {
		content = f.results[f.callIdx]
	}
	f.callIdx++
	return []providers.Message{
		{Role: "assistant", Content: content},
	}, nil
}

func TestSelfEvaluateLoop_ConfidenceHigh_AcceptsImmediately(t *testing.T) {
	reflector := &fakeReflector{shouldReflect: true, rootCause: "none", hypothesis: "fine"}
	loop := NewSelfEvaluateLoop(reflector, nil, nil)
	loop.MinConfidence = 0.0 // anything passes

	history := []providers.Message{
		{Role: "user", Content: "write tests"},
		{Role: "assistant", Content: "done, all tests pass"},
	}

	outcome, ref, fc := loop.Evaluate("test", history, "deepseek-v4-pro", 1)

	if outcome != IterAccepted {
		t.Fatalf("expected IterAccepted, got %s", outcome)
	}
	if ref != nil {
		t.Errorf("expected nil reflection, got %+v", ref)
	}
	if fc != nil {
		t.Errorf("expected nil fault chain, got %v", fc)
	}

	accepted, revised, deferred := loop.Stats()
	if accepted != 1 || revised != 0 || deferred != 0 {
		t.Errorf("stats: accepted=%d revised=%d deferred=%d", accepted, revised, deferred)
	}
}

func TestSelfEvaluateLoop_LowConfidence_Revises(t *testing.T) {
	reflector := &fakeReflector{
		shouldReflect: true,
		rootCause:     "tests didn't pass",
		hypothesis:    "fix the test assertions",
	}
	loop := NewSelfEvaluateLoop(reflector, nil, nil)
	loop.MinConfidence = 1.0 // impossible to meet

	history := []providers.Message{
		{Role: "user", Content: "write tests"},
		{Role: "assistant", Content: "done"},
	}

	outcome, ref, _ := loop.Evaluate("test", history, "deepseek-v4-pro", 1)

	if outcome != IterRevised {
		t.Fatalf("expected IterRevised, got %s", outcome)
	}
	if ref == nil {
		t.Fatal("expected non-nil reflection")
	}
	if ref.RootCause != "tests didn't pass" {
		t.Errorf("expected root cause 'tests didn't pass', got %q", ref.RootCause)
	}

	accepted, revised, deferred := loop.Stats()
	if accepted != 0 || revised != 1 || deferred != 0 {
		t.Errorf("stats: accepted=%d revised=%d deferred=%d", accepted, revised, deferred)
	}
}

func TestSelfEvaluateLoop_MaxIterations_Defers(t *testing.T) {
	reflector := &fakeReflector{
		shouldReflect: true,
		rootCause:     "persistent failure",
		hypothesis:    "fundamental issue",
	}
	loop := NewSelfEvaluateLoop(reflector, nil, nil)
	loop.MinConfidence = 1.0
	loop.MaxIterations = 2

	history := []providers.Message{
		{Role: "user", Content: "complex task"},
	}

	outcome, ref, _ := loop.Evaluate("complex", history, "deepseek-v4-pro", 2)

	if outcome != IterDeferred {
		t.Fatalf("expected IterDeferred, got %s", outcome)
	}
	if ref == nil {
		t.Fatal("expected non-nil reflection on defer")
	}

	accepted, revised, deferred := loop.Stats()
	if accepted != 0 || revised != 0 {
		t.Errorf("expected 0 accepted and 0 revised, got accepted=%d revised=%d", accepted, revised)
	}
	if deferred != 1 {
		t.Errorf("expected 1 deferred, got %d", deferred)
	}
}

func TestSelfEvaluateLoop_NoReflector_RevisesWithoutReflection(t *testing.T) {
	loop := NewSelfEvaluateLoop(nil, nil, nil)
	loop.MinConfidence = 1.0

	history := []providers.Message{
		{Role: "user", Content: "task"},
	}

	outcome, ref, _ := loop.Evaluate("task", history, "deepseek-v4-pro", 1)

	if outcome != IterRevised {
		t.Fatalf("expected IterRevised, got %s", outcome)
	}
	if ref != nil {
		t.Errorf("expected nil reflection (no reflector), got %+v", ref)
	}
}

func TestSelfEvaluateLoop_NoReflector_MaxIterations_Defers(t *testing.T) {
	loop := NewSelfEvaluateLoop(nil, nil, nil)
	loop.MinConfidence = 1.0

	history := []providers.Message{
		{Role: "user", Content: "task"},
	}

	outcome, ref, _ := loop.Evaluate("task", history, "deepseek-v4-pro", 6)

	if outcome != IterDeferred {
		t.Fatalf("expected IterDeferred, got %s", outcome)
	}
	if ref != nil {
		t.Errorf("expected nil reflection, got %+v", ref)
	}
}

func TestSelfEvaluateLoop_Defaults(t *testing.T) {
	loop := NewSelfEvaluateLoop(nil, nil, nil)

	if loop.MaxIterations != 6 {
		t.Errorf("expected MaxIterations=6, got %d", loop.MaxIterations)
	}
	if loop.MinConfidence != 0.7 {
		t.Errorf("expected MinConfidence=0.7, got %f", loop.MinConfidence)
	}
	if loop.MaxRevisionDepth != 3 {
		t.Errorf("expected MaxRevisionDepth=3, got %d", loop.MaxRevisionDepth)
	}
}

func TestBuildRevisionContext(t *testing.T) {
	loop := NewSelfEvaluateLoop(nil, nil, nil)

	reflection := &Reflection{
		RootCause:  "test timeout",
		Hypothesis: "add retry logic",
		Confidence: 0.3,
	}

	ctx := loop.BuildRevisionContext(reflection, []string{"step1 failed", "step2 timeout"}, nil)

	if ctx == "" {
		t.Fatal("expected non-empty revision context")
	}
	if !strContains(ctx, "test timeout") {
		t.Error("expected root cause in context")
	}
	if !strContains(ctx, "add retry logic") {
		t.Error("expected hypothesis in context")
	}
	if !strContains(ctx, "step1 failed") {
		t.Error("expected fault chain in context")
	}
}

func TestBuildRevisionContext_TruncatesHistory(t *testing.T) {
	loop := NewSelfEvaluateLoop(nil, nil, nil)
	loop.MaxRevisionDepth = 2

	history := []RevisionEntry{
		{Iteration: 1, RootCause: "r1", Hypothesis: "h1"},
		{Iteration: 2, RootCause: "r2", Hypothesis: "h2"},
		{Iteration: 3, RootCause: "r3", Hypothesis: "h3"},
		{Iteration: 4, RootCause: "r4", Hypothesis: "h4"},
		{Iteration: 5, RootCause: "r5", Hypothesis: "h5"},
	}

	ctx := loop.BuildRevisionContext(nil, nil, history)

	// Only last 2 should appear.
	if strContains(ctx, "r3") {
		t.Error("iteration 3 should have been truncated")
	}
	if !strContains(ctx, "r4") {
		t.Error("iteration 4 should be present")
	}
	if !strContains(ctx, "r5") {
		t.Error("iteration 5 should be present")
	}
}

func TestRevisionPrompt(t *testing.T) {
	loop := NewSelfEvaluateLoop(nil, nil, nil)

	ref := &Reflection{
		RootCause:  "compile error",
		Hypothesis: "fix type mismatch",
	}

	prompt := loop.RevisionPrompt(3, 6, ref)

	if !strContains(prompt, "SELF-REVISION 3/6") {
		t.Error("expected iteration counter")
	}
	if !strContains(prompt, "compile error") {
		t.Error("expected root cause")
	}
	if !strContains(prompt, "type mismatch") {
		t.Error("expected hypothesis")
	}
	if !strContains(prompt, "DIFFERENT approach") {
		t.Error("expected different approach directive")
	}
}

func TestExtractTaskType(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Phase 1: Write tests", "test"},
		{"Fix the build", "fix"},
		{"Refactor database layer", "refactor"},
		{"Migrate config", "migrate"},
		{"Deploy to production", "deploy"},
		{"Setup CI pipeline", "setup"},
		{"Something else entirely", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			phase := &PlanPhase{Title: tt.title}
			got := extractTaskType(phase)
			if got != tt.want {
				t.Errorf("extractTaskType(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

func TestAutoMode_ShouldSelfEvaluate(t *testing.T) {
	t.Run("nil AutoMode", func(t *testing.T) {
		var am *AutoMode
		if am.ShouldSelfEvaluate() {
			t.Error("nil AutoMode should not self-evaluate")
		}
	})

	t.Run("no SelfEval wired", func(t *testing.T) {
		am := NewAutoMode("auto")
		if am.ShouldSelfEvaluate() {
			t.Error("no SelfEval wired should not self-evaluate")
		}
	})

	t.Run("SelfEval wired but not auto level", func(t *testing.T) {
		am := NewAutoMode("yolo")
		am.SetSelfEval(NewSelfEvaluateLoop(nil, nil, nil))
		if am.ShouldSelfEvaluate() {
			t.Error("yolo mode should not self-evaluate")
		}
	})

	t.Run("SelfEval wired in auto level", func(t *testing.T) {
		am := NewAutoMode("auto")
		am.SetSelfEval(NewSelfEvaluateLoop(nil, nil, nil))
		if !am.ShouldSelfEvaluate() {
			t.Error("auto mode with SelfEval should self-evaluate")
		}
	})
}

func TestRunPhase_FirstPassAccepted(t *testing.T) {
	reflector := &fakeReflector{shouldReflect: true}
	loop := NewSelfEvaluateLoop(reflector, nil, nil)
	loop.MinConfidence = 0.0 // always accept
	loop.MaxIterations = 3

	runner := &fakeRunner{results: []string{"all tests pass"}}
	result, history, err := loop.RunPhase(
		context.Background(),
		&PlanPhase{Title: "Phase 1: Write tests", Description: "write unit tests"},
		"deepseek-v4-pro",
		runner.run,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Accepted {
		t.Fatal("expected accepted")
	}
	if result.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", result.Iterations)
	}
	if len(history) == 0 {
		t.Error("expected non-empty history")
	}
}

func TestRunPhase_MultipleIterations_Accepted(t *testing.T) {
	reflector := &fakeReflector{
		shouldReflect: true,
		rootCause:     "tests failing",
		hypothesis:    "fix assertions",
	}
	loop := NewSelfEvaluateLoop(reflector, nil, nil)
	// Set confidence so first attempt fails (confidence too low), second passes.
	callCount := 0
	origFn := AssessConfidence
	defer func() {
		// Not restoring — test-only override is fine in a test.
	}()

	loop.SetConfidenceFn(func(taskType string, history []providers.Message, model string) *ConfidenceAssessment {
		_ = origFn
		callCount++
		if callCount == 1 {
			return &ConfidenceAssessment{Level: ConfidenceLow, Score: 0.3, Reasoning: "first try"}
		}
		return &ConfidenceAssessment{Level: ConfidenceHigh, Score: 0.8, Reasoning: "fixed"}
	})
	loop.MaxIterations = 3

	runner := &fakeRunner{results: []string{"first attempt", "second attempt"}}
	result, _, err := loop.RunPhase(
		context.Background(),
		&PlanPhase{Title: "Fix the build", Description: "fix compilation errors"},
		"deepseek-v4-pro",
		runner.run,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Accepted {
		t.Fatal("expected accepted after revision")
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", result.Iterations)
	}
}

func TestRunPhase_ExhaustsIterations_Defers(t *testing.T) {
	reflector := &fakeReflector{
		shouldReflect: true,
		rootCause:     "fundamental issue",
		hypothesis:    "cannot fix within constraints",
	}
	loop := NewSelfEvaluateLoop(reflector, nil, nil)
	loop.MaxIterations = 2
	loop.MinConfidence = 1.0 // impossible

	runner := &fakeRunner{
		results: []string{"attempt 1", "attempt 2", "attempt 3"}, // extra just in case
	}
	result, _, err := loop.RunPhase(
		context.Background(),
		&PlanPhase{Title: "Refactor everything", Description: "big refactor"},
		"deepseek-v4-pro",
		runner.run,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Accepted {
		t.Fatal("expected not accepted (deferred)")
	}
	if result.Iterations != 2 {
		t.Errorf("expected 2 iterations (max), got %d", result.Iterations)
	}
}

func TestSelfEvaluateLoop_RecordLearning(t *testing.T) {
	recorder := &fakeLearningRecorder{}
	loop := NewSelfEvaluateLoop(nil, nil, recorder)

	loop.RecordLearning("test_failure")

	if len(recorder.recorded) != 1 || recorder.recorded[0] != "test_failure" {
		t.Errorf("expected [test_failure], got %v", recorder.recorded)
	}
}

func TestSelfEvaluateLoop_RecordLearning_NilRecorder(t *testing.T) {
	loop := NewSelfEvaluateLoop(nil, nil, nil)
	// Should not panic.
	loop.RecordLearning("test")
}

func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
