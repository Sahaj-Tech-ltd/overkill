package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

type ownedWorkAgent struct {
	fakeAgent
	started  chan struct{}
	finished chan struct{}
	calls    atomic.Int32
	active   atomic.Int32
	panicRun bool
}

func (a *ownedWorkAgent) Stream(ctx context.Context, _ string) (<-chan agent.StreamEvent, error) {
	a.calls.Add(1)
	if a.panicRun {
		panic("controlled dispatch panic")
	}
	a.active.Add(1)
	defer a.active.Add(-1)
	a.started <- struct{}{}
	<-ctx.Done()
	a.finished <- struct{}{}
	return nil, ctx.Err()
}
func lifecyclePost(t *testing.T, b *Bridge, chat, text string) int {
	t.Helper()
	payload, _ := json.Marshal(InboundPayload{Channel: "fixture", ChatKey: chat, From: "owner", Text: text})
	response, err := (&http.Client{Timeout: 2 * time.Second}).Post(b.Addr()+"/v1/in", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}
func startOwnedBridge(t *testing.T, a *ownedWorkAgent, limit int, timeout time.Duration) (*Bridge, context.CancelFunc, <-chan error) {
	t.Helper()
	router, err := gateway.NewSessionRouter("")
	if err != nil {
		t.Fatal(err)
	}
	d := gateway.NewDispatcher(a, router)
	d.MaxReqsPerMinute = 1000
	b := New(d, "", "127.0.0.1:0")
	b.MaxConcurrentInbound = limit
	b.WorkTimeout = timeout
	ctx, cancel := context.WithCancel(context.Background())
	done, err := b.Start(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	return b, cancel, done
}
func waitOwned(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal(label + " timed out")
	}
}
func TestBridgeOwnedWorkShutdownWaitsAndRejectsOverflow(t *testing.T) {
	a := &ownedWorkAgent{started: make(chan struct{}, 4), finished: make(chan struct{}, 4)}
	b, cancel, done := startOwnedBridge(t, a, 2, time.Minute)
	// Random session IDs can collide in the dispatcher's bounded striped
	// serialization pool. These distinct fixed bindings keep this capacity
	// fixture from deliberately queueing its second stream behind the first.
	for index := range 2 {
		if err := b.Dispatcher.Router.Bind("bridge:fixture", fmt.Sprint(index), "", fmt.Sprintf("owned-session-%d", index)); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 2 {
		if status := lifecyclePost(t, b, fmt.Sprint(index), "hold"); status != http.StatusAccepted {
			t.Fatalf("admit: %d", status)
		}
		waitOwned(t, a.started, "work start")
	}
	// There is no unbounded waiting ingress queue: rejected requests never start
	// a dispatch goroutine, provider call or tool invocation.
	for index := range 100 {
		if status := lifecyclePost(t, b, fmt.Sprintf("overflow-%d", index), "hold"); status != http.StatusTooManyRequests {
			t.Fatalf("overflow accepted: %d", status)
		}
	}
	if a.calls.Load() != 2 || a.active.Load() != 2 || len(b.workSlots) != 2 {
		t.Fatalf("overflow created work: calls=%d active=%d slots=%d", a.calls.Load(), a.active.Load(), len(b.workSlots))
	}
	// An already-busy chat is rejected before 202 as well.
	if status := lifecyclePost(t, b, "0", "more work"); status != http.StatusTooManyRequests {
		t.Fatalf("busy chat accepted: %d", status)
	}
	// Control commands have a separate finite reserve even with all turn slots held.
	if status := lifecyclePost(t, b, "0", "/stop"); status != http.StatusAccepted {
		t.Fatalf("control rejected: %d", status)
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not wait/cancel accepted work")
	}
	if a.active.Load() != 0 || len(b.workSlots) != 0 {
		t.Fatal("accepted work outlived shutdown")
	}
	waitOwned(t, a.finished, "first cancellation")
	waitOwned(t, a.finished, "second cancellation")
}
func TestBridgeWorkDeadlineSurvivesHTTPResponse(t *testing.T) {
	a := &ownedWorkAgent{started: make(chan struct{}, 1), finished: make(chan struct{}, 1)}
	b, cancel, done := startOwnedBridge(t, a, 1, 150*time.Millisecond)
	if status := lifecyclePost(t, b, "deadline", "hold"); status != http.StatusAccepted {
		t.Fatal(status)
	}
	waitOwned(t, a.started, "work start")
	select {
	case <-a.finished:
		t.Fatal("accepted work inherited completed HTTP request cancellation")
	case <-time.After(20 * time.Millisecond):
	}
	waitOwned(t, a.finished, "work deadline")
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
}
func TestBridgePanicReleasesAdmissionAndKeepsServerAvailable(t *testing.T) {
	a := &ownedWorkAgent{panicRun: true}
	b, cancel, done := startOwnedBridge(t, a, 1, time.Second)
	subscriber, frames := b.subscribe("bridge:fixture")
	defer b.unsubscribe("bridge:fixture", subscriber)
	for index := range 2 {
		if status := lifecyclePost(t, b, fmt.Sprint(index), "panic"); status != http.StatusAccepted {
			t.Fatal(status)
		}
		end := time.Now().Add(time.Second)
		for len(b.workSlots) != 0 && time.Now().Before(end) {
			time.Sleep(time.Millisecond)
		}
		if len(b.workSlots) != 0 {
			t.Fatal("panic leaked admission")
		}
		sawError := false
		deadline := time.After(time.Second)
		for !sawError {
			select {
			case frame := <-frames:
				sawError = frame.Kind == "error"
			case <-deadline:
				t.Fatal("panic not surfaced as an error")
			}
		}
	}
	if !b.Healthy() || a.calls.Load() != 2 {
		t.Fatal("panic broke bridge")
	}
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatal(err)
	}
}
func TestBridgeRejectsInboundBeforeLifecycleStarts(t *testing.T) {
	b := New(gateway.NewDispatcher(&fakeAgent{}, nil), "", "")
	payload := []byte(`{"channel":"fixture","chat":"a","text":"hello"}`)
	request, _ := http.NewRequest(http.MethodPost, "http://unused/v1/in", bytes.NewReader(payload))
	recorder := newLifecycleRecorder()
	b.handleIn(recorder, request)
	if recorder.status != http.StatusServiceUnavailable {
		t.Fatal(recorder.status)
	}
}

// A minimal recorder keeps this lifecycle check independent of network startup.
type lifecycleRecorder struct {
	headers http.Header
	status  int
}

func newLifecycleRecorder() *lifecycleRecorder      { return &lifecycleRecorder{headers: make(http.Header)} }
func (r *lifecycleRecorder) Header() http.Header    { return r.headers }
func (r *lifecycleRecorder) WriteHeader(status int) { r.status = status }
func (r *lifecycleRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return len(data), nil
}
