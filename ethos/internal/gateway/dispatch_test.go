package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
)

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
	_ = r.Follow("42", "tui")
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
