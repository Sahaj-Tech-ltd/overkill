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

type panickingSink struct {
	name       string
	namePanics bool
	value      any
}

func (sink *panickingSink) Name() string {
	if sink.namePanics {
		panic(sink.value)
	}
	return sink.name
}
func (sink *panickingSink) Send(context.Context, CompletionEvent) error { panic(sink.value) }

func TestEmitterAllPanickingSinksFailWithStructuredErrors(t *testing.T) {
	cause := errors.New("delivery panic")
	err := NewEmitter(&panickingSink{name: "first", value: cause}, &panickingSink{name: "second", value: "second panic"}).Emit(t.Context(), sampleEvent())
	if err == nil {
		t.Fatal("recovery must not count as delivery success")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("panic cause lost: %v", err)
	}
	var panicError *SinkPanicError
	if !errors.As(err, &panicError) || panicError.Sink != "first" {
		t.Fatalf("missing sink identity: %v", err)
	}
}

func TestEmitterMixedPanickingAndErrorSinksFail(t *testing.T) {
	cause := errors.New("normal sink error")
	err := NewEmitter(&panickingSink{name: "panic", value: "boom"}, &stubSink{name: "normal", sendErr: cause}).Emit(t.Context(), sampleEvent())
	if !errors.Is(err, cause) {
		t.Fatalf("normal failure lost: %v", err)
	}
	var panicError *SinkPanicError
	if !errors.As(err, &panicError) {
		t.Fatalf("panic failure lost: %v", err)
	}
}

func TestEmitterHealthySinkStillSucceedsAlongsidePanic(t *testing.T) {
	healthy := &stubSink{name: "healthy"}
	if err := NewEmitter(&panickingSink{name: "panic", value: "boom"}, healthy).Emit(t.Context(), sampleEvent()); err != nil {
		t.Fatalf("healthy delivery must succeed: %v", err)
	}
	if healthy.calls != 1 {
		t.Fatalf("healthy sink called %d times", healthy.calls)
	}
}

func TestEmitterPanickingSinkNameIsFailure(t *testing.T) {
	err := NewEmitter(&panickingSink{namePanics: true, value: "name panic"}).Emit(t.Context(), sampleEvent())
	var panicError *SinkPanicError
	if !errors.As(err, &panicError) || panicError.Sink != "sink[0]" {
		t.Fatalf("name panic must retain fallback identity: %v", err)
	}
}
