package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
)

// visionImg locally aliases vision.Image so the fake describer's
// signature matches the real interface without ceremony.
type visionImg = vision.Image

type fakeAgent struct {
	mu        sync.Mutex
	sessionID string
	feed      []agent.StreamEvent
	gotInput  string
}

func (f *fakeAgent) SessionID() string { f.mu.Lock(); defer f.mu.Unlock(); return f.sessionID }
func (f *fakeAgent) SetSessionID(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionID = id
}
func (f *fakeAgent) Stream(_ context.Context, in string) (<-chan agent.StreamEvent, error) {
	f.mu.Lock()
	f.gotInput = in
	feed := f.feed
	f.mu.Unlock()
	ch := make(chan agent.StreamEvent, len(feed))
	for _, ev := range feed {
		ch <- ev
	}
	close(ch)
	return ch, nil
}
func (f *fakeAgent) EStop()                                       { f.mu.Lock(); defer f.mu.Unlock(); f.sessionID = "" }
func (f *fakeAgent) Interrupt()                                   {}
func (f *fakeAgent) SetQuestionFunc(agent.QuestionFunc)           {}
func (f *fakeAgent) Undo() (string, error)                        { return "undone", nil }
func (f *fakeAgent) Retry(ctx context.Context) (string, error)    { return "retried", nil }
func (f *fakeAgent) Steer(msg string) string                      { return "steering queued: " + msg }
func (f *fakeAgent) Fork(name string) (string, error)             { return "fork-" + name, nil }
func (f *fakeAgent) Snapshot(name string) (string, error)         { return "snap-" + name, nil }
func (f *fakeAgent) Rollback(n int) (string, error)               { return "rolled back", nil }
func (f *fakeAgent) Snapshots() (string, error)                   { return "1 checkpoint(s)", nil }
func (f *fakeAgent) SetGoal(_ context.Context, text string) error { return nil }
func (f *fakeAgent) GetGoal(_ context.Context) (string, error)    { return "", nil }
func (f *fakeAgent) PauseGoal(_ context.Context) error            { return nil }
func (f *fakeAgent) ResumeGoal(_ context.Context) error           { return nil }
func (f *fakeAgent) ClearGoal(_ context.Context) error            { return nil }
func (f *fakeAgent) Compact(_ context.Context) (*agent.CompactResult, error) {
	return &agent.CompactResult{TokensBefore: 100, TokensAfter: 50, Summary: "compacted"}, nil
}
func (f *fakeAgent) ExportHistory(path string) (string, error) { return path, nil }
func (f *fakeAgent) SetThinkingLevel(level string)             {}
func (f *fakeAgent) ThinkingLevel() string                     { return "off" }
func (f *fakeAgent) Mode() string                              { return "build" }
func (f *fakeAgent) IsBusy() bool                              { return false }

type capturedFrame struct {
	kind string
	text string
}

type fakeReply struct {
	mu     sync.Mutex
	frames []capturedFrame
}

func (r *fakeReply) PostInitial(_ context.Context, _ Inbound, text string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, capturedFrame{"post", text})
	return "h1", nil
}
func (r *fakeReply) Update(_ context.Context, _ string, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, capturedFrame{"update", text})
	return nil
}
func (r *fakeReply) Final(_ context.Context, _ string, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, capturedFrame{"final", text})
	return nil
}
func (r *fakeReply) Error(_ context.Context, _ string, err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, capturedFrame{"error", err.Error()})
	return nil
}
func (r *fakeReply) StartTyping() (stop func()) { return func() {} }

func waitFinal(t *testing.T, r *fakeReply) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, f := range r.frames {
			if f.kind == "final" || f.kind == "error" {
				r.mu.Unlock()
				return f.text
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("never saw final/error frame")
	return ""
}

func TestDispatch_NewChatBindsAndStreams(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{
		{Type: agent.EventToken, Content: "hello "},
		{Type: agent.EventToken, Content: "world"},
	}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 5 * time.Millisecond

	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "42", Text: "hi",
	}, reply)
	final := waitFinal(t, reply)

	if !strings.Contains(final, "hello world") {
		t.Fatalf("final %q missing tokens", final)
	}
	if a.SessionID() == "" {
		t.Fatal("session id never set")
	}
	if got, _ := r.Resolve("telegram", "42", "", ""); got == "" {
		t.Fatal("router never bound the chat")
	}
}

func TestDispatch_FollowTUIRoutesToLiveSession(t *testing.T) {
	a := &fakeAgent{sessionID: "tui-live", feed: []agent.StreamEvent{
		{Type: agent.EventToken, Content: "ok"},
	}}
	r, _ := NewSessionRouter("")
	_ = r.Follow("telegram", "42", "tui")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 5 * time.Millisecond

	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "42", Text: "ping",
	}, reply)
	_ = waitFinal(t, reply)

	if a.SessionID() != "tui-live" {
		t.Fatalf("session id = %q want tui-live", a.SessionID())
	}
}

func TestDispatch_HelpCommand(t *testing.T) {
	d := NewDispatcher(&fakeAgent{}, nil)
	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{Channel: "x", ChatKey: "1", Text: "/help"}, reply)
	final := waitFinal(t, reply)
	if !strings.Contains(final, "/sessions") || !strings.Contains(final, "/follow") {
		t.Fatalf("help text missing key commands: %q", final)
	}
}

type fakeDescriber struct{ desc string }

func (f *fakeDescriber) Describe(_ context.Context, _ []visionImg, _ string) (string, error) {
	return f.desc, nil
}

func TestDispatch_VisionPrependsCaption(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ack"}}}
	d := NewDispatcher(a, nil)
	d.UpdateEvery = 5 * time.Millisecond
	d.Vision = &fakeDescriber{desc: "a login form"}

	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "1",
		Text:   "what is this?",
		Images: []InboundImage{{Bytes: []byte("x"), Mime: "image/png"}},
	}, reply)
	_ = waitFinal(t, reply)

	if !strings.Contains(a.gotInput, "a login form") {
		t.Fatalf("agent input %q should contain caption", a.gotInput)
	}
	if !strings.Contains(a.gotInput, "what is this?") {
		t.Fatalf("agent input %q should contain user text", a.gotInput)
	}
}

func TestDispatch_AttachBindsExplicitly(t *testing.T) {
	r, _ := NewSessionRouter("")
	d := NewDispatcher(&fakeAgent{}, r)
	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{Channel: "telegram", ChatKey: "42", Text: "/attach my-sess"}, reply)
	_ = waitFinal(t, reply)

	got, _ := r.Resolve("telegram", "42", "", "")
	if got != "my-sess" {
		t.Fatalf("attach: got %q want my-sess", got)
	}
}

// TestDispatch_SessionIDRace exercises the TOCTOU between resolveSession
// (reading Agent.SessionID) and runTurn (writing Agent.SetSessionID).
// Bug #32: resolveSession reads SessionID under agentMu, releases, then
// passes the (now possibly stale) value to Router.Resolve. Concurrent
// runTurn in another goroutine can change SessionID between the read
// and the Router.Resolve call, causing incorrect session resolution.
func TestDispatch_SessionIDRace(t *testing.T) {
	a := &fakeAgent{
		sessionID: "live-session",
		feed: []agent.StreamEvent{
			{Type: agent.EventToken, Content: "ok"},
		},
	}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 5 * time.Millisecond

	// Fire multiple concurrent Handle calls to exercise the race.
	const n = 10
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			reply := &fakeReply{}
			d.Handle(context.Background(), Inbound{
				Channel: "telegram", ChatKey: "42", Text: "ping",
			}, reply)
			_ = waitFinal(t, reply)
			errs <- nil
		}()
	}
	for i := 0; i < n; i++ {
		<-errs
	}
}

// TestDispatch_ResolveSessionTOCTOU directly tests that resolveSession
// and concurrent SetSessionID calls are properly synchronized.
// Bug #32: resolveSession releases agentMu before Router.Resolve uses
// the live value, allowing a concurrent SetSessionID to change the
// Agent's session between the read and the use.
//
// With the fix (agentMu held across entire resolveSession), the race
// detector finds no concurrent read/write on the sessionID state.
// Without the fix, concurrent reads (in resolveSession) and writes
// (in runTurn) race unsynchronized when they use different striped locks.
func TestDispatch_ResolveSessionTOCTOU(t *testing.T) {
	a := &fakeAgent{
		sessionID: "initial-session",
		feed: []agent.StreamEvent{
			{Type: agent.EventToken, Content: "ok"},
		},
	}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = time.Millisecond

	// Run many iterations of concurrent read+write on SessionID.
	// Each iteration: reader reads SessionID under agentMu, writer
	// sets SessionID under agentMu. The race detector verifies that
	// both access the same memory only under the mutex.
	const iterations = 500
	var wg sync.WaitGroup

	for i := 0; i < iterations; i++ {
		wg.Add(2)

		// Reader goroutine: simulates resolveSession reading SessionID
		go func() {
			defer wg.Done()
			d.agentMu.Lock()
			_ = a.SessionID()
			d.agentMu.Unlock()
		}()

		// Writer goroutine: simulates runTurn setting SessionID
		go func() {
			defer wg.Done()
			d.agentMu.Lock()
			a.SetSessionID("updated-session")
			d.agentMu.Unlock()
		}()
	}

	wg.Wait()
}
