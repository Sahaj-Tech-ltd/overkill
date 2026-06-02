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
// Adversarial stress tests: Gateway dispatch with nil pointers,
// boundary inputs, Unicode spam, and concurrent access.
// ==========================================================================

// G-STRESS-1: Handle with nil Agent - should not panic
func TestStress_HandleNilAgent(t *testing.T) {
	r, _ := NewSessionRouter("")
	d := NewDispatcher(nil, r) // nil agent
	d.Agent = nil
	d.UpdateEvery = 5 * time.Millisecond

	reply := &fakeReply{}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Handle with nil Agent: %v", r)
		}
	}()
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "test", Text: "hello",
	}, reply)
}

// G-STRESS-2: Handle with nil Router - should not panic
func TestStress_HandleNilRouter(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	d := NewDispatcher(a, nil)
	d.Router = nil
	d.UpdateEvery = 5 * time.Millisecond

	reply := &fakeReply{}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Handle with nil Router: %v", r)
		}
	}()
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "test", Text: "hello",
	}, reply)
	_ = waitFinal(t, reply)
}

// G-STRESS-3: Empty text, no images - should silently return
func TestStress_HandleEmptyMessage(t *testing.T) {
	d := NewDispatcher(&fakeAgent{}, nil)
	reply := &fakeReply{}

	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "empty", Text: "",
	}, reply)

	reply.mu.Lock()
	n := len(reply.frames)
	reply.mu.Unlock()
	if n > 0 {
		t.Errorf("empty message produced %d frames (expected 0)", n)
	}
}

// G-STRESS-4: Whitespace-only text
func TestStress_HandleWhitespaceOnly(t *testing.T) {
	d := NewDispatcher(&fakeAgent{}, nil)
	reply := &fakeReply{}

	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "ws", Text: "   \t  \n  ",
	}, reply)

	reply.mu.Lock()
	n := len(reply.frames)
	reply.mu.Unlock()
	if n > 0 {
		t.Errorf("whitespace-only message produced %d frames (expected 0)", n)
	}
}

// G-STRESS-5: Near-max-length text (100KB)
func TestStress_HandleLargeText(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 5 * time.Millisecond

	largeText := strings.Repeat("Hello world! ", 8000) // ~100KB
	reply := &fakeReply{}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Handle with large text: %v", r)
		}
	}()
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "large", Text: largeText,
	}, reply)
	_ = waitFinal(t, reply)
	t.Logf("Large text handled OK, agent input len=%d", len(a.gotInput))
}

// G-STRESS-6: Unicode emoji spam (1000 emojis)
func TestStress_HandleEmojiSpam(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 5 * time.Millisecond

	emoji := strings.Repeat("😀🎉🔥💩🦀✨🌈🍕🦄💀", 100) // 1000 emojis
	reply := &fakeReply{}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Handle with emoji spam: %v", r)
		}
	}()
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "emoji", Text: emoji,
	}, reply)
	_ = waitFinal(t, reply)
	t.Logf("Emoji spam handled OK, agent input len=%d", len(a.gotInput))
}

// G-STRESS-7: Zero-width and control character spam
func TestStress_HandleControlCharacters(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 5 * time.Millisecond

	// Mix of zero-width joiners, RTL markers, bidi controls
	ctrlText := "hello\u200D\u200B\u200C\u202E\u2066\u2069world\uFEFF\u00ADtest"
	ctrlText += strings.Repeat("\x00", 100) // NUL bytes

	reply := &fakeReply{}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Handle with control chars: %v", r)
		}
	}()
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "ctrl", Text: ctrlText,
	}, reply)
	_ = waitFinal(t, reply)
}

// G-STRESS-8: Concurrent Handle calls on same session (race stress)
func TestStress_HandleConcurrent(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "resp"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 5 * time.Millisecond

	// Bind session first
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "concurrent", Text: "/new",
	}, &fakeReply{})

	var wg sync.WaitGroup
	panics := make(chan interface{}, 20)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panics <- r
				}
			}()
			reply := &fakeReply{}
			d.Handle(context.Background(), Inbound{
				Channel: "telegram", ChatKey: "concurrent", Text: fmt.Sprintf("msg %d", n),
			}, reply)
		}(i)
	}
	wg.Wait()
	close(panics)

	for p := range panics {
		t.Errorf("PANIC: concurrent Handle: %v", p)
	}
}

// G-STRESS-9: NUL bytes in session key fields
func TestStress_HandleNULInKeys(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 5 * time.Millisecond

	// NUL bytes in channel, chatKey, thread
	nulChannel := "telegram\x00injected"
	nulChatKey := "chat\x0042"
	nulThread := "thread\x00evil"

	reply := &fakeReply{}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Handle with NUL bytes: %v", r)
		}
	}()
	d.Handle(context.Background(), Inbound{
		Channel: nulChannel, ChatKey: nulChatKey, Thread: nulThread, Text: "hello",
	}, reply)
	_ = waitFinal(t, reply)
}

// G-STRESS-10: Long slash command with trailing garbage
func TestStress_HandleLongSlashCommand(t *testing.T) {
	d := NewDispatcher(&fakeAgent{}, nil)
	reply := &fakeReply{}

	longCmd := "/help" + strings.Repeat(" extra-garbage-data-that-should-not-crash", 500)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Handle with long slash command: %v", r)
		}
	}()
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "cmd", Text: longCmd,
	}, reply)
	_ = waitFinal(t, reply)
}

// G-STRESS-11: Canceled context
func TestStress_HandleCanceledContext(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{
		{Type: agent.EventToken, Content: "t1"},
		{Type: agent.EventToken, Content: "t2"},
		{Type: agent.EventToken, Content: "t3"},
	}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately canceled

	reply := &fakeReply{}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Handle with canceled context: %v", r)
		}
	}()
	d.Handle(ctx, Inbound{
		Channel: "telegram", ChatKey: "cancel", Text: "test",
	}, reply)
}

// G-STRESS-12: Empty From field (no sender)
func TestStress_HandleEmptySender(t *testing.T) {
	a := &fakeAgent{feed: []agent.StreamEvent{{Type: agent.EventToken, Content: "ok"}}}
	r, _ := NewSessionRouter("")
	d := NewDispatcher(a, r)
	d.UpdateEvery = 5 * time.Millisecond

	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "nosender", Text: "hello", From: "",
	}, reply)
	_ = waitFinal(t, reply)
}

// G-STRESS-13: Extremely long session ID in attach command
func TestStress_HandleAttachLongID(t *testing.T) {
	r, _ := NewSessionRouter("")
	d := NewDispatcher(&fakeAgent{}, r)
	reply := &fakeReply{}

	longID := "/attach " + strings.Repeat("x", 10000)
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "long", Text: longID,
	}, reply)
	_ = waitFinal(t, reply)
}

// G-STRESS-14: checkRateLimit concurrent flood
func TestStress_RateLimitConcurrent(t *testing.T) {
	d := NewDispatcher(&fakeAgent{}, nil)

	var wg sync.WaitGroup
	allowed := make(chan bool, 1000)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				allowed <- d.checkRateLimit("test-sender")
			}
		}()
	}
	wg.Wait()
	close(allowed)

	trueCount := 0
	for ok := range allowed {
		if ok {
			trueCount++
		}
	}
	t.Logf("Rate limit: %d/1000 allowed", trueCount)
}

// G-STRESS-15: stripeFor with empty session ID
func TestStress_StripeForEmptyID(t *testing.T) {
	d := NewDispatcher(&fakeAgent{}, nil)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: stripeFor with empty ID: %v", r)
		}
	}()
	mu := d.stripeFor("")
	_ = mu
}

// G-STRESS-16: stripeFor with extremely long session ID
func TestStress_StripeForLongID(t *testing.T) {
	d := NewDispatcher(&fakeAgent{}, nil)
	longID := strings.Repeat("abcdefghij", 10000) // 100KB ID
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: stripeFor with long ID: %v", r)
		}
	}()
	mu := d.stripeFor(longID)
	_ = mu
}

// G-STRESS-17: runTurn with nil Agent
func TestStress_RunTurnNilAgent(t *testing.T) {
	d := NewDispatcher(nil, nil)
	d.Agent = nil
	reply := &fakeReply{}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: runTurn with nil Agent: %v", r)
		}
	}()
	d.runTurn(context.Background(), Inbound{Channel: "test", ChatKey: "t"}, reply, "sid", "hello", 0)
}

// G-STRESS-18: NewSessionID uniqueness under concurrent generation
func TestStress_NewSessionIDUniqueness(t *testing.T) {
	const n = 1000
	ids := make(map[string]bool, n)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := NewSessionID("test")
			mu.Lock()
			ids[id] = true
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(ids) != n {
		t.Errorf("Session ID collision: %d unique out of %d generated", len(ids), n)
	}
}

// G-STRESS-19: SessionRouter with NUL bytes in key fields
func TestStress_RouterNULKeys(t *testing.T) {
	r, _ := NewSessionRouter("")
	channel := "telegram\x00hack"
	chatKey := "chat\x00inject"
	thread := "thread\x00evil"

	if err := r.Bind(channel, chatKey, thread, "sess-nul"); err != nil {
		t.Fatalf("Bind with NUL bytes: %v", err)
	}

	sid, follow := r.Resolve(channel, chatKey, thread, "")
	if sid != "sess-nul" {
		t.Errorf("Resolve with NUL keys: got %q, want sess-nul", sid)
	}
	_ = follow
}

// G-STRESS-20: Router Recent with large limit
func TestStress_RouterRecentHugeLimit(t *testing.T) {
	r, _ := NewSessionRouter("")
	for i := 0; i < 5; i++ {
		_ = r.Bind("ch", fmt.Sprintf("key%d", i), "", fmt.Sprintf("sess%d", i))
	}
	// Request 1M entries
	result := r.Recent(1_000_000)
	if len(result) > 5 {
		t.Errorf("Recent(1M) returned %d entries, expected at most 5", len(result))
	}
}
