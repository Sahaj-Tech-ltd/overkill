package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// fakeNeverEndingProvider keeps the agent's stream loop spinning so
// we can interrupt mid-flight. Each Stream call returns a channel
// that NEVER closes — the cancel path is the only way out.
type fakeNeverEndingProvider struct{}

func (f *fakeNeverEndingProvider) Name() string { return "never-ending" }
func (f *fakeNeverEndingProvider) Complete(ctx context.Context, req providers.Request) (providers.Response, error) {
	return providers.Response{}, nil
}
func (f *fakeNeverEndingProvider) Stream(ctx context.Context, req providers.Request) (<-chan providers.Chunk, error) {
	ch := make(chan providers.Chunk)
	go func() {
		defer close(ch)
		<-ctx.Done()
	}()
	return ch, nil
}
func (f *fakeNeverEndingProvider) Models() []providers.Model { return nil }

// TestCheckpointInterrupt_OnCtxCancel proves that cancelling the
// stream's context saves a flow record with the cancelled_by_user
// reason — the core G1 contract.
func TestCheckpointInterrupt_OnCtxCancel(t *testing.T) {
	store := NewMemoryFlowStore()
	a := New(Config{
		Provider:  &fakeNeverEndingProvider{},
		Model:     "test",
		MaxSteps:  5,
		SessionID: "sess-1",
	})
	a.SetFlowStore(store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := a.Stream(ctx, "refactor the auth module please")
	if err != nil {
		t.Fatal(err)
	}

	// Let the stream loop spin up, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Drain until the EventError lands.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			if ev.Type == EventError {
				goto done
			}
		case <-deadline:
			t.Fatal("timed out waiting for EventError after cancel")
		}
	}
done:

	// Allow the deferred checkpoint to complete before assertion.
	time.Sleep(50 * time.Millisecond)

	all, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 flow record after cancel, got %d", len(all))
	}
	got := all[0]
	// Either outer-select cancel ("cancelled_by_user") or inner-stream
	// cancel ("cancelled_by_user_mid_stream") is correct — depends on
	// whether the cancel happens between iterations or during a
	// provider stream. Both go through isInterruptReason and produce
	// the same resume note.
	if !isInterruptReason(got.Reason) {
		t.Errorf("reason: %q not classified as interrupt", got.Reason)
	}
	if !strings.HasPrefix(got.Reason, "cancelled_by_user") {
		t.Errorf("reason: %q should describe a user cancel", got.Reason)
	}
	if got.SessionID != "sess-1" {
		t.Errorf("session: %q", got.SessionID)
	}
	if got.UserInput != "refactor the auth module please" {
		t.Errorf("user input lost: %q", got.UserInput)
	}
}

func TestCheckpointInterrupt_OnEStop(t *testing.T) {
	store := NewMemoryFlowStore()
	a := New(Config{
		Provider:  &fakeNeverEndingProvider{},
		Model:     "test",
		MaxSteps:  5,
		SessionID: "sess-estop",
	})
	a.SetFlowStore(store, nil)

	ch, err := a.Stream(context.Background(), "long task")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	a.EStop()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			if ev.Type == EventError {
				goto done
			}
		case <-deadline:
			t.Fatal("timed out waiting for EventError after estop")
		}
	}
done:
	time.Sleep(50 * time.Millisecond)
	all, _ := store.List()
	if len(all) != 1 || all[0].Reason != "halted_by_estop" {
		t.Errorf("expected halted_by_estop record, got %+v", all)
	}
}

func TestCheckpointInterrupt_NoStoreIsNoOp(t *testing.T) {
	a := New(Config{
		Provider:  &fakeNeverEndingProvider{},
		Model:     "test",
		MaxSteps:  5,
		SessionID: "sess-nostore",
	})
	// FlowStore deliberately not set — must not panic on cancel.
	ctx, cancel := context.WithCancel(context.Background())
	_, err := a.Stream(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	// Sleep so any deferred panic surfaces in this test, not a
	// later one.
	time.Sleep(50 * time.Millisecond)
}

func TestConsumeInterruptNote_SurfacesAndClears(t *testing.T) {
	store := NewMemoryFlowStore()
	a := New(Config{Model: "test", SessionID: "sess-x"})
	a.SetFlowStore(store, nil)

	// Pre-seed a cancelled-by-user record.
	state := &FlowState{
		ID:        "f1",
		SessionID: "sess-x",
		UserInput: "fix the bug in payments.go",
		Step:      3,
		History:   []providers.Message{{Role: "user", Content: "fix the bug in payments.go"}},
		Reason:    "cancelled_by_user",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	note := a.consumeInterruptNote()
	if !strings.Contains(note, "cancelled") {
		t.Errorf("note should describe cancel reason: %q", note)
	}
	if !strings.Contains(note, "payments.go") {
		t.Errorf("note should include original input: %q", note)
	}

	// Record must be cleared so the next turn doesn't replay it.
	if got, _ := store.Load("f1"); got != nil {
		t.Error("interrupt record should be deleted after consumption")
	}

	// Second call returns empty — nothing to surface.
	if again := a.consumeInterruptNote(); again != "" {
		t.Errorf("second call should be empty: %q", again)
	}
}

func TestConsumeInterruptNote_IgnoresOtherSessions(t *testing.T) {
	store := NewMemoryFlowStore()
	a := New(Config{Model: "test", SessionID: "sess-A"})
	a.SetFlowStore(store, nil)

	// Different-session record — must NOT surface.
	_ = store.Save(&FlowState{
		ID:        "f-other",
		SessionID: "sess-B",
		Reason:    "cancelled_by_user",
		UserInput: "other session's task",
		CreatedAt: time.Now().UTC(),
	})

	if note := a.consumeInterruptNote(); note != "" {
		t.Errorf("cross-session leak: %q", note)
	}
	// Other-session record must remain.
	if got, _ := store.Load("f-other"); got == nil {
		t.Error("other-session record should not be deleted")
	}
}

func TestConsumeInterruptNote_IgnoresMaxStepsExhaustion(t *testing.T) {
	// Max-steps exhaustion has its OWN resume path (alarm-driven
	// daemon resume). We should NOT also surface it as an interrupt
	// note — that would double-fire the resume.
	store := NewMemoryFlowStore()
	a := New(Config{Model: "test", SessionID: "sess-y"})
	a.SetFlowStore(store, nil)

	_ = store.Save(&FlowState{
		ID:        "f1",
		SessionID: "sess-y",
		Reason:    "exceeded max steps (50)",
		UserInput: "huge task",
		CreatedAt: time.Now().UTC(),
	})

	if note := a.consumeInterruptNote(); note != "" {
		t.Errorf("max-steps record should not surface as interrupt note: %q", note)
	}
	// Record must remain so the alarm-driven resume can pick it up.
	if got, _ := store.Load("f1"); got == nil {
		t.Error("max-steps record should NOT be deleted by interrupt-note path")
	}
}

func TestConsumeInterruptNote_NewestWins(t *testing.T) {
	store := NewMemoryFlowStore()
	a := New(Config{Model: "test", SessionID: "sess-z"})
	a.SetFlowStore(store, nil)

	old := &FlowState{
		ID: "old", SessionID: "sess-z", Reason: "cancelled_by_user",
		UserInput: "old task", CreatedAt: time.Now().Add(-time.Hour).UTC(),
	}
	newer := &FlowState{
		ID: "newer", SessionID: "sess-z", Reason: "cancelled_by_user",
		UserInput: "newer task", CreatedAt: time.Now().UTC(),
	}
	_ = store.Save(old)
	_ = store.Save(newer)

	note := a.consumeInterruptNote()
	if !strings.Contains(note, "newer task") {
		t.Errorf("newest record should win: %q", note)
	}
}

func TestIsInterruptReason(t *testing.T) {
	if !isInterruptReason("cancelled_by_user") {
		t.Error("cancelled_by_user")
	}
	if !isInterruptReason("cancelled_by_user_mid_stream") {
		t.Error("mid_stream")
	}
	if !isInterruptReason("halted_by_estop") {
		t.Error("estop")
	}
	if isInterruptReason("exceeded max steps (50)") {
		t.Error("max-steps should NOT be classified as interrupt")
	}
	if isInterruptReason("") {
		t.Error("empty reason")
	}
}

func TestFormatInterruptNote_StaysShort(t *testing.T) {
	state := &FlowState{
		Reason:    "cancelled_by_user",
		Step:      5,
		UserInput: strings.Repeat("very long input ", 100),
		History:   make([]providers.Message, 12),
	}
	note := formatInterruptNote(state)
	if len(note) > 800 {
		t.Errorf("note should stay terse, got %d chars", len(note))
	}
	if !strings.Contains(note, "...") {
		t.Errorf("long input should be truncated with ellipsis: %q", note)
	}
	if !strings.Contains(note, "5/12") {
		t.Errorf("step/history count missing: %q", note)
	}
}

func TestFormatInterruptNote_NilSafe(t *testing.T) {
	if got := formatInterruptNote(nil); got != "" {
		t.Errorf("nil state should produce empty note: %q", got)
	}
}
