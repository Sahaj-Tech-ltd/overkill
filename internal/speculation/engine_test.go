package speculation

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewEngine(t *testing.T) {
	e := NewEngine()
	if e.State() != StateIdle {
		t.Errorf("initial state: got %s, want idle", e.State())
	}
	if e.MaxTurns != 20 {
		t.Errorf("MaxTurns: got %d, want 20", e.MaxTurns)
	}
	if e.MaxDuration != 30*time.Second {
		t.Errorf("MaxDuration: got %v, want 30s", e.MaxDuration)
	}
	if e.Result() != nil {
		t.Error("result should be nil initially")
	}
}

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateIdle, "idle"},
		{StateRunning, "running"},
		{StateReady, "ready"},
		{StateAccepted, "accepted"},
		{StateDiscarded, "discarded"},
		{State(99), "unknown(99)"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String(): got %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestStart_NoOnSpeculate(t *testing.T) {
	e := NewEngine()
	e.Start() // should be no-op
	if e.State() != StateIdle {
		t.Errorf("state after Start with no OnSpeculate: got %s, want idle", e.State())
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	e := NewEngine()
	e.mu.Lock()
	e.state = StateRunning
	e.mu.Unlock()

	e.Start() // should be no-op
	if e.State() != StateRunning {
		t.Errorf("state after double Start: got %s", e.State())
	}
}

func TestDiscard_FromIdle(t *testing.T) {
	e := NewEngine()
	changed := false
	e.OnStateChange = func(old, new State) { changed = true }

	e.Discard()
	if e.State() != StateDiscarded {
		t.Errorf("state after Discard: got %s, want discarded", e.State())
	}
	if changed {
		t.Error("OnStateChange should not fire when discarding from idle")
	}
}

func TestDiscard_FiresCallback(t *testing.T) {
	e := NewEngine()
	e.mu.Lock()
	e.state = StateRunning
	e.mu.Unlock()

	var oldState, newState State
	e.OnStateChange = func(old, new State) { oldState = old; newState = new }

	e.Discard()
	if e.State() != StateDiscarded {
		t.Errorf("state: got %s", e.State())
	}
	if oldState != StateRunning || newState != StateDiscarded {
		t.Errorf("OnStateChange: old=%s new=%s, want running→discarded", oldState, newState)
	}
}

func TestAccept_NotReady(t *testing.T) {
	e := NewEngine()
	// Should return nil because state is not Ready.
	r := e.Accept()
	if r != nil {
		t.Error("Accept on non-ready state should return nil")
	}
}

func TestAccept_Ready(t *testing.T) {
	e := NewEngine()
	expectedResult := &Result{Summary: "predicted action"}
	e.mu.Lock()
	e.state = StateReady
	e.result = expectedResult
	e.mu.Unlock()

	var oldState, newState State
	e.OnStateChange = func(old, new State) { oldState = old; newState = new }

	r := e.Accept()
	if r != expectedResult {
		t.Fatal("Accept did not return the result")
	}
	if e.State() != StateAccepted {
		t.Errorf("state after Accept: got %s, want accepted", e.State())
	}
	if oldState != StateReady || newState != StateAccepted {
		t.Errorf("OnStateChange: old=%s new=%s, want ready→accepted", oldState, newState)
	}
}

func TestReset(t *testing.T) {
	e := NewEngine()
	e.mu.Lock()
	e.state = StateAccepted
	e.result = &Result{Summary: "x"}
	e.mu.Unlock()

	e.Reset()
	if e.State() != StateIdle {
		t.Errorf("state after Reset: got %s, want idle", e.State())
	}
	if e.Result() != nil {
		t.Error("result should be nil after Reset")
	}
}

func TestReset_AlreadyIdle(t *testing.T) {
	e := NewEngine()
	changed := false
	e.OnStateChange = func(old, new State) { changed = true }

	e.Reset() // should be no-op
	if e.State() != StateIdle {
		t.Errorf("state: got %s", e.State())
	}
	if changed {
		t.Error("OnStateChange should not fire when resetting from idle")
	}
}

func TestReadOnlyTools(t *testing.T) {
	// Verify the read-only toolset contains expected tools.
	for _, tool := range []string{"read", "grep", "glob", "lsp", "list"} {
		if !ReadOnlyTools[tool] {
			t.Errorf("expected %q in ReadOnlyTools", tool)
		}
	}
	// Write tools should NOT be present.
	for _, tool := range []string{"write", "shell", "edit", "patch", "exec"} {
		if ReadOnlyTools[tool] {
			t.Errorf("write tool %q should NOT be in ReadOnlyTools", tool)
		}
	}
}

func TestStartToReady_Lifecycle(t *testing.T) {
	e := NewEngine()
	e.MaxDuration = 100 * time.Millisecond

	stateChanges := make(chan [2]State, 3)
	e.OnStateChange = func(old, new State) {
		stateChanges <- [2]State{old, new}
	}

	e.OnSpeculate = func(ctx context.Context) (*Result, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return &Result{Summary: "fast prediction", StartedAt: time.Now(), CompletedAt: time.Now()}, nil
		}
	}

	e.Start()
	time.Sleep(50 * time.Millisecond) // wait for speculation to complete

	if e.State() != StateReady {
		t.Fatalf("state after speculation: got %s, want ready", e.State())
	}
	if e.Result() == nil {
		t.Fatal("result is nil after speculation completed")
	}
	if e.Result().Summary != "fast prediction" {
		t.Errorf("result summary: got %q", e.Result().Summary)
	}
}

func TestSpeculation_ErrorDiscards(t *testing.T) {
	e := NewEngine()
	e.MaxDuration = 200 * time.Millisecond

	e.OnSpeculate = func(ctx context.Context) (*Result, error) {
		return nil, errors.New("speculation failed")
	}

	e.Start()
	time.Sleep(100 * time.Millisecond)

	// After error, state should be discarded.
	if e.State() != StateDiscarded {
		t.Errorf("state after failed speculation: got %s, want discarded", e.State())
	}
}

func TestSpeculation_TimeoutDiscards(t *testing.T) {
	e := NewEngine()
	e.MaxDuration = 10 * time.Millisecond

	e.OnSpeculate = func(ctx context.Context) (*Result, error) {
		time.Sleep(100 * time.Millisecond) // longer than max duration
		return &Result{Summary: "too slow"}, nil
	}

	e.Start()
	time.Sleep(50 * time.Millisecond)

	// After timeout, state should be discarded.
	if e.State() != StateDiscarded {
		t.Errorf("state after timeout: got %s, want discarded", e.State())
	}
}

func TestSpeculation_DiscardDuringRun(t *testing.T) {
	e := NewEngine()
	e.MaxDuration = 5 * time.Second

	started := make(chan struct{})
	e.OnSpeculate = func(ctx context.Context) (*Result, error) {
		started <- struct{}{}
		<-ctx.Done() // block until discarded
		return nil, ctx.Err()
	}

	e.Start()
	<-started // wait for speculation to begin
	e.Discard()

	if e.State() != StateDiscarded {
		t.Errorf("state after mid-run Discard: got %s, want discarded", e.State())
	}
}

// TestSpeculation_ResetVsRun_Race proves bug #39:
// When Reset() is called while a speculation goroutine is in-flight,
// the stale result can be written back AFTER Reset completes,
// resurrecting a result that should have been cleared.
// The bug: run() checks e.state == StateDiscarded but Reset sets
// state to StateIdle — missing this case allows stale writes.
func TestSpeculation_ResetVsRun_Race(t *testing.T) {
	// Run multiple iterations to increase chance of hitting the race,
	// since goroutine scheduling is non-deterministic.
	for i := 0; i < 50; i++ {
		e := NewEngine()
		e.MaxDuration = 5 * time.Second

		// Speculation returns immediately so result is in the buffered
		// channel before Reset has a chance to cancel the context.
		e.OnSpeculate = func(ctx context.Context) (*Result, error) {
			return &Result{Summary: "stale result"}, nil
		}

		e.Start()

		// Clear cancelFn BEFORE Reset so Reset won't cancel the context.
		// This way run() is still waiting on the select when Reset
		// sets state back to Idle. The result is already in resultCh.
		e.mu.Lock()
		e.cancelFn = nil
		e.mu.Unlock()

		e.Reset()

		// Give run() time to pick up the stale result from resultCh.
		time.Sleep(20 * time.Millisecond)

		if e.State() == StateReady {
			t.Errorf("BUG #39 (iter %d): state is Ready after Reset — stale result resurrected", i)
			return
		}
		if e.Result() != nil {
			t.Errorf("BUG #39 (iter %d): result is non-nil after Reset — stale result resurrected", i)
			return
		}
	}
}
