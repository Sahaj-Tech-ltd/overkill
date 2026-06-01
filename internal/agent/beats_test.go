package agent

import (
	"sync"
	"testing"
)

type fakeBeatRecorder struct {
	mu    sync.Mutex
	calls []beatCall
}

type beatCall struct {
	Type, Context, Session string
}

func (f *fakeBeatRecorder) RecordBeat(t, ctx, sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, beatCall{t, ctx, sid})
}

func (f *fakeBeatRecorder) seen() []beatCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]beatCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestRecordBeat_NilRecorderSafe(t *testing.T) {
	a := &Agent{_sessionID: "x"}
	// Should not panic.
	a.recordBeat(BeatFirstFailure, "boom")
}

func TestRecordBeat_FiresOnFirstFailure(t *testing.T) {
	a := &Agent{_sessionID: "sess1"}
	r := &fakeBeatRecorder{}
	a.SetBeatRecorder(r)
	a.recordBeat(BeatFirstFailure, "compile error")
	got := r.seen()
	if len(got) != 1 {
		t.Fatalf("expected 1 beat, got %d", len(got))
	}
	if got[0].Type != BeatFirstFailure {
		t.Errorf("type = %q, want %q", got[0].Type, BeatFirstFailure)
	}
	if got[0].Session != "sess1" {
		t.Errorf("session not propagated: %q", got[0].Session)
	}
}

func TestRecordBeat_PanicRecovered(t *testing.T) {
	a := &Agent{_sessionID: "x"}
	a.SetBeatRecorder(panickyRecorder{})
	// Must not propagate.
	a.recordBeat(BeatFirstSuccess, "ok")
}

type panickyRecorder struct{}

func (panickyRecorder) RecordBeat(string, string, string) { panic("intentional") }

func TestSetBeatRecorder_Nil(t *testing.T) {
	a := &Agent{_sessionID: "x"}
	a.SetBeatRecorder(&fakeBeatRecorder{})
	a.SetBeatRecorder(nil)
	// Subsequent recordBeat must be a no-op (no panic).
	a.recordBeat(BeatFirstFailure, "x")
}
