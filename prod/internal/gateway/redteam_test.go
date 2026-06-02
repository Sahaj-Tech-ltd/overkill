package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
)

// ==========================================================================
// RED TEAM: Gateway dispatch — crash/race/overflow attacks
// Uses existing fakeAgent/fakeReply from dispatch_test.go
// ==========================================================================

// RT-GW-1: Concurrent Handle calls — race on active map, session routing.
func TestRedTeam_Gateway_ConcurrentHandle(t *testing.T) {
	ag := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(ag, r)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			in := Inbound{
				Channel: "telegram",
				ChatKey: fmt.Sprintf("chat-%d", n%3),
				From:    fmt.Sprintf("user-%d", n),
				Text:    fmt.Sprintf("msg-%d from concurrent test", n),
			}
			reply := &fakeReply{}
			d.Handle(context.Background(), in, reply)
		}(i)
	}
	wg.Wait()
}

// RT-GW-2: Handle with nil agent — verify no panic.
func TestRedTeam_Gateway_NilAgent(t *testing.T) {
	r, _ := NewSessionRouter("")
	d := NewDispatcher(nil, r)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC with nil agent: %v", r)
		}
	}()

	in := Inbound{Channel: "test", ChatKey: "chat-1", From: "u1", Text: "hello"}
	reply := &fakeReply{}
	d.Handle(context.Background(), in, reply)
}

// RT-GW-3: Handle with nil router — verify no panic.
func TestRedTeam_Gateway_NilRouter(t *testing.T) {
	ag := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	d := NewDispatcher(ag, nil)

	in := Inbound{Channel: "test", ChatKey: "chat-1", From: "u1", Text: "hello"}
	reply := &fakeReply{}
	d.Handle(context.Background(), in, reply)
}

// RT-GW-4: Extremely long message.
func TestRedTeam_Gateway_LongMessage(t *testing.T) {
	ag := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(ag, r)

	longMsg := strings.Repeat("x", 200_000)
	in := Inbound{Channel: "test", ChatKey: "chat-1", From: "u1", Text: longMsg}
	reply := &fakeReply{}
	d.Handle(context.Background(), in, reply)
}

// RT-GW-5: Slash commands with nil agent — verify no panic.
func TestRedTeam_Gateway_SlashCmdNilAgent(t *testing.T) {
	r, _ := NewSessionRouter("")
	d := NewDispatcher(nil, r)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC on slash cmd with nil agent: %v", r)
		}
	}()

	cmds := []string{"/stop", "/estop", "/undo", "/retry", "/steer test", "/fork branch"}
	for _, cmd := range cmds {
		in := Inbound{Channel: "test", ChatKey: "chat-1", From: "u1", Text: cmd}
		reply := &fakeReply{}
		d.Handle(context.Background(), in, reply)
	}
}

// RT-GW-6: Slash commands with nil router — verify no panic.
func TestRedTeam_Gateway_SlashCmdNilRouter(t *testing.T) {
	ag := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	d := NewDispatcher(ag, nil)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC on slash cmd with nil router: %v", r)
		}
	}()

	cmds := []string{"/attach abc", "/follow xyz", "/new", "/end"}
	for _, cmd := range cmds {
		in := Inbound{Channel: "test", ChatKey: "chat-1", From: "u1", Text: cmd}
		reply := &fakeReply{}
		d.Handle(context.Background(), in, reply)
	}
}

// RT-GW-7: Handle with cancelled context.
func TestRedTeam_Gateway_CancelledContext(t *testing.T) {
	ag := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(ag, r)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	in := Inbound{Channel: "test", ChatKey: "chat-1", From: "u1", Text: "hello"}
	reply := &fakeReply{}
	d.Handle(ctx, in, reply)
}

// RT-GW-8: Dispatcher with SessionTitler callback.
func TestRedTeam_Gateway_SessionTitler(t *testing.T) {
	ag := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(ag, r)

	titled := make(chan string, 1)
	d.SessionTitler = func(sessionID, firstMsg string) {
		titled <- firstMsg
	}

	in := Inbound{Channel: "test", ChatKey: "chat-1", From: "u1", Text: "auto-title-me"}
	reply := &fakeReply{}
	d.Handle(context.Background(), in, reply)

	select {
	case msg := <-titled:
		t.Logf("SessionTitler called: %q", msg)
	case <-time.After(2 * time.Second):
		t.Error("SessionTitler was never called")
	}
}

// RT-GW-9: Unicode/emoji bomb in message.
func TestRedTeam_Gateway_UnicodeBomb(t *testing.T) {
	ag := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "👍"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(ag, r)

	bomb := "🔥💥🎯" + "\x00\x1b[31mRED\x1b[0m" + "\U0010FFFF" + strings.Repeat("🚀", 500)
	in := Inbound{Channel: "test", ChatKey: "chat-1", From: "u1", Text: bomb}
	reply := &fakeReply{}
	d.Handle(context.Background(), in, reply)
}

// RT-GW-10: Rapid slash command spam.
func TestRedTeam_Gateway_SlashCmdSpam(t *testing.T) {
	ag := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(ag, r)

	for i := 0; i < 50; i++ {
		in := Inbound{Channel: "test", ChatKey: "chat-1", From: "u1", Text: "/help"}
		reply := &fakeReply{}
		d.Handle(context.Background(), in, reply)
	}
}
