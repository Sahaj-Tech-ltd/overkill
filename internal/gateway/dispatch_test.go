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
func (f *fakeAgent) EStop()     { f.mu.Lock(); defer f.mu.Unlock(); f.sessionID = "" }
func (f *fakeAgent) Interrupt() {}
func (f *fakeAgent) SetQuestionFunc(agent.QuestionFunc) {}
func (f *fakeAgent) Undo() (string, error)              { return "undone", nil }
func (f *fakeAgent) Retry() (string, error)             { return "retried", nil }
func (f *fakeAgent) Steer(msg string) string            { return "steering queued: " + msg }
func (f *fakeAgent) Fork(name string) (string, error)   { return "fork-" + name, nil }
func (f *fakeAgent) Snapshot(name string) (string, error) { return "snap-" + name, nil }
func (f *fakeAgent) Rollback(n int) (string, error)       { return "rolled back", nil }
func (f *fakeAgent) Snapshots() (string, error)           { return "1 checkpoint(s)", nil }
func (f *fakeAgent) SetGoal(_ context.Context, text string) error  { return nil }
func (f *fakeAgent) GetGoal(_ context.Context) (string, error)     { return "", nil }
func (f *fakeAgent) PauseGoal(_ context.Context) error             { return nil }
func (f *fakeAgent) ResumeGoal(_ context.Context) error            { return nil }
func (f *fakeAgent) ClearGoal(_ context.Context) error             { return nil }
func (f *fakeAgent) Compact(_ context.Context) (*agent.CompactResult, error) {
	return &agent.CompactResult{TokensBefore: 100, TokensAfter: 50, Summary: "compacted"}, nil
}
func (f *fakeAgent) ExportHistory(path string) (string, error)     { return path, nil }
func (f *fakeAgent) SetThinkingLevel(level string)                  {}
func (f *fakeAgent) ThinkingLevel() string                          { return "off" }
func (f *fakeAgent) Mode() string                                   { return "build" }
func (f *fakeAgent) IsBusy() bool                                   { return false }

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
