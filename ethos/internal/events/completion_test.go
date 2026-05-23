package events

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubSink is a test double that either succeeds or returns a fixed error.
type stubSink struct {
	name    string
	sendErr error
	calls   int
}

func (s *stubSink) Name() string { return s.name }
func (s *stubSink) Send(_ context.Context, _ CompletionEvent) error {
	s.calls++
	return s.sendErr
}

func sampleEvent() CompletionEvent {
	return CompletionEvent{
		SessionID:  "sess-1",
		Intent:     "write a hello world",
		Outcome:    "success",
		Artefacts:  []Artefact{{Kind: "file", Ref: "main.go"}},
		DurationMs: 1200,
		CostUSD:    0.003,
		EmittedAt:  time.Now(),
	}
}

func TestEmitter_FansOutToAllSinks(t *testing.T) {
	a := &stubSink{name: "a"}
	b := &stubSink{name: "b"}
	e := NewEmitter(a, b)

	if err := e.Emit(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if a.calls != 1 {
		t.Errorf("sink a: expected 1 call, got %d", a.calls)
	}
	if b.calls != 1 {
		t.Errorf("sink b: expected 1 call, got %d", b.calls)
	}
}

func TestEmitter_ReturnsNilWhenAtLeastOneSinkSucceeds(t *testing.T) {
	bad := &stubSink{name: "bad", sendErr: errors.New("boom")}
	good := &stubSink{name: "good"}
	e := NewEmitter(bad, good)

	if err := e.Emit(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("expected nil when one sink succeeds, got %v", err)
	}
}

func TestEmitter_ReturnsErrorWhenAllSinksFail(t *testing.T) {
	a := &stubSink{name: "a", sendErr: errors.New("a-fail")}
	b := &stubSink{name: "b", sendErr: errors.New("b-fail")}
	e := NewEmitter(a, b)

	err := e.Emit(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("expected error when all sinks fail, got nil")
	}
	if !errors.Is(err, a.sendErr) {
		t.Errorf("combined error should wrap a-fail, got: %v", err)
	}
	if !errors.Is(err, b.sendErr) {
		t.Errorf("combined error should wrap b-fail, got: %v", err)
	}
}

func TestEmitter_NoSinksIsNoop(t *testing.T) {
	e := NewEmitter()
	if err := e.Emit(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("expected nil for empty emitter, got %v", err)
	}
}

func TestEmitter_RespectsSinkTimeout(t *testing.T) {
	// A sink that blocks longer than the 10-second per-sink deadline. We
	// drive it with a pre-cancelled context so the outer Emit returns fast.
	slow := &blockingSink{block: make(chan struct{})}
	e := NewEmitter(slow)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled — sink's derived context is immediately done

	// Emit must return quickly (not block forever) when the context is done.
	done := make(chan error, 1)
	go func() { done <- e.Emit(ctx, sampleEvent()) }()

	select {
	case <-done:
		// good — returned promptly
	case <-time.After(3 * time.Second):
		t.Fatal("Emit blocked longer than expected on cancelled context")
	}
	close(slow.block)
}

type blockingSink struct{ block chan struct{} }

func (s *blockingSink) Name() string { return "blocking" }
func (s *blockingSink) Send(ctx context.Context, _ CompletionEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.block:
		return nil
	}
}
