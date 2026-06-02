package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func TestFlowID_StableForSameInputs(t *testing.T) {
	a := flowIDFor("session-abc", "do the thing")
	b := flowIDFor("session-abc", "do the thing")
	if a != b {
		t.Errorf("flowID not stable: %s vs %s", a, b)
	}
}

func TestFlowID_DiffersByInput(t *testing.T) {
	a := flowIDFor("s", "task A")
	b := flowIDFor("s", "task B")
	if a == b {
		t.Errorf("inputs A and B should produce distinct IDs, both got %s", a)
	}
}

func TestFlowID_DiffersBySession(t *testing.T) {
	a := flowIDFor("s1", "task")
	b := flowIDFor("s2", "task")
	if a == b {
		t.Errorf("sessions s1 and s2 should produce distinct IDs, both got %s", a)
	}
}

func TestCheckpointFlow_PersistsState(t *testing.T) {
	store := NewMemoryFlowStore()
	history := []providers.Message{
		{Role: "user", Content: "fix the bug"},
		{Role: "assistant", Content: "looking at the file"},
	}
	state, err := CheckpointFlow(store, "f1", "sess-1", "fix the bug", "claude-3-5", "anthropic", history, 50, "exceeded budget")
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if state.Step != 50 {
		t.Errorf("step: %d", state.Step)
	}
	if len(state.History) != 2 {
		t.Errorf("history not preserved: %d msgs", len(state.History))
	}
	if state.Resumes != 0 {
		t.Errorf("fresh flow should start with 0 resumes, got %d", state.Resumes)
	}

	// Reload.
	loaded, err := store.Load("f1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Reason != "exceeded budget" {
		t.Errorf("reason lost: %q", loaded.Reason)
	}
}

func TestCheckpointFlow_OverwritePreservesResumeCount(t *testing.T) {
	store := NewMemoryFlowStore()
	// First checkpoint.
	_, _ = CheckpointFlow(store, "f1", "", "task", "m", "", nil, 50, "first")
	// Resume once.
	_, _ = MarkResumed(store, "f1")
	// Second checkpoint (resumed task timed out again).
	state, err := CheckpointFlow(store, "f1", "", "task", "m", "", nil, 50, "second")
	if err != nil {
		t.Fatal(err)
	}
	if state.Resumes != 1 {
		t.Errorf("expected Resumes=1 after one MarkResumed, got %d", state.Resumes)
	}
}

func TestMarkResumed_BumpsAndStamps(t *testing.T) {
	store := NewMemoryFlowStore()
	_, _ = CheckpointFlow(store, "f1", "", "task", "m", "", nil, 50, "x")
	state, err := MarkResumed(store, "f1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Resumes != 1 {
		t.Errorf("Resumes: %d", state.Resumes)
	}
	if len(state.ResumedAt) != 1 {
		t.Errorf("ResumedAt: %v", state.ResumedAt)
	}
	if time.Since(state.ResumedAt[0]) > time.Minute {
		t.Errorf("ResumedAt not recent: %v", state.ResumedAt[0])
	}
}

func TestMarkResumed_ExhaustedAfterMaxResumes(t *testing.T) {
	store := NewMemoryFlowStore()
	_, _ = CheckpointFlow(store, "f1", "", "task", "m", "", nil, 50, "x")
	for i := 0; i < MaxResumes; i++ {
		if _, err := MarkResumed(store, "f1"); err != nil {
			t.Fatalf("resume %d: %v", i+1, err)
		}
	}
	// MaxResumes+1 should fail.
	state, err := MarkResumed(store, "f1")
	if !errors.Is(err, ErrFlowExhausted) {
		t.Errorf("expected ErrFlowExhausted on resume %d, got %v", MaxResumes+1, err)
	}
	if state == nil || state.Resumes != MaxResumes {
		t.Errorf("state should still surface so caller can report final count: %+v", state)
	}
}

func TestMarkResumed_NotFound(t *testing.T) {
	store := NewMemoryFlowStore()
	if _, err := MarkResumed(store, "missing"); err == nil {
		t.Error("expected error for missing flow")
	}
}

func TestLoad_CorruptReturnsSentinel(t *testing.T) {
	store := NewMemoryFlowStore()
	store.saveRaw("corrupt", []byte("{not json"))
	_, err := store.Load("corrupt")
	if !errors.Is(err, ErrFlowCorrupt) {
		t.Errorf("expected ErrFlowCorrupt, got %v", err)
	}
}

func TestList_DropsCorruptEntries(t *testing.T) {
	store := NewMemoryFlowStore()
	_ = store.Save(&FlowState{ID: "good", UserInput: "task", CreatedAt: time.Now()})
	store.saveRaw("bad", []byte("garbage"))

	got, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "good" {
		t.Errorf("List should drop corrupt entries, got %+v", got)
	}
	// Second List should not re-see the corrupt entry (it was cleaned).
	got2, _ := store.List()
	if len(got2) != 1 {
		t.Errorf("corrupt entry should have been pruned: %d", len(got2))
	}
}

func TestFormatResumePrompt_AndExtract(t *testing.T) {
	id := "flow-abc-123"
	prompt := FormatResumePrompt(id)
	got := ExtractFlowID(prompt)
	if got != id {
		t.Errorf("roundtrip failed: %q → %q", id, got)
	}
}

func TestExtractFlowID_NotAResumePrompt(t *testing.T) {
	if got := ExtractFlowID("regular alarm prompt"); got != "" {
		t.Errorf("non-resume prompt should return empty, got %q", got)
	}
}

func TestExtractFlowID_TrimsWhitespace(t *testing.T) {
	id := ExtractFlowID("  overkill:flow:resume:abc  ")
	if id != "abc" {
		t.Errorf("got %q", id)
	}
}

func TestStore_DeleteRemoves(t *testing.T) {
	store := NewMemoryFlowStore()
	_ = store.Save(&FlowState{ID: "f1", UserInput: "x", CreatedAt: time.Now()})
	if err := store.Delete("f1"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Load("f1")
	if got != nil {
		t.Errorf("after delete, Load should return nil, got %+v", got)
	}
}

func TestSave_EmptyIDErrors(t *testing.T) {
	store := NewMemoryFlowStore()
	if err := store.Save(&FlowState{}); err == nil {
		t.Error("empty ID should error")
	}
}
