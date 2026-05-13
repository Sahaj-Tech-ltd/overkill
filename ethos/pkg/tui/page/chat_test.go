package page

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	tuitypes "github.com/Sahaj-Tech-ltd/overkill/pkg/tui/types"
)

func TestChatPage_Init(t *testing.T) {
	p := NewChatPage(nil)
	cmd := p.Init()
	if cmd == nil {
		t.Error("Init should return cmd")
	}
}

func TestChatPage_UpdateWindowSize(t *testing.T) {
	p := NewChatPage(nil)
	updated, _ := p.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if updated.width != 100 || updated.height != 40 {
		t.Error("size not set")
	}
}

func TestChatPage_SendMessage(t *testing.T) {
	p := NewChatPage(nil)
	updated, _ := p.Update(tuitypes.SendMsg{Text: "hello"})
	updated, _ = p.Update(tuitypes.AgentStreamMsg{Chunk: "world"})
	updated, _ = p.Update(tuitypes.AgentResponseMsg{Content: "done", Done: true})
	updated, _ = p.Update(tuitypes.AgentResponseMsg{Err: errors.New("fail")})
	if updated.messages.Len() == 0 {
		t.Error("should have error message")
	}
}

func TestChatPage_EditorFocus(t *testing.T) {
	p := NewChatPage(nil)
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes})
	_ = cmd
}

// TestChatPage_CancelStream verifies the stream cancel hook: when CancelStream
// is called with no stream in flight, it's a safe no-op. When a cancel func is
// installed (simulating an active stream), it fires and clears the slot.
func TestChatPage_CancelStream(t *testing.T) {
	p := NewChatPage(nil)
	if got := p.CancelStream(); got {
		t.Error("CancelStream on idle page should return false")
	}

	fired := false
	_, cancel := context.WithCancel(context.Background())
	p.streamCancel = func() {
		fired = true
		cancel()
	}
	if got := p.CancelStream(); !got {
		t.Error("CancelStream with active stream should return true")
	}
	if !fired {
		t.Error("CancelStream did not invoke the stored cancel func")
	}
	if p.streamCancel != nil {
		t.Error("streamCancel should be cleared after firing")
	}
	// Second call after cancel cleared: must be safe.
	if got := p.CancelStream(); got {
		t.Error("CancelStream after clear should return false")
	}
}

// TestChatPage_QueueOnBusy: SendMsg while busy pushes to pendingQueue
// instead of starting a parallel stream. Multiple sends stack FIFO.
func TestChatPage_QueueOnBusy(t *testing.T) {
	p := NewChatPage(nil)
	// Simulate busy without going through real agent.Stream — we just
	// flip the flag so the SendMsg branch routes to the queue.
	p.busy = true
	p.agent = &agent.Agent{} // non-nil so SendMsg doesn't bail early

	for _, txt := range []string{"first", "second", "third"} {
		p2, _ := p.Update(tuitypes.SendMsg{Text: txt})
		p = p2
	}

	if p.QueueDepth() != 3 {
		t.Fatalf("expected 3 queued, got %d", p.QueueDepth())
	}
	if p.pendingQueue[0] != "first" || p.pendingQueue[2] != "third" {
		t.Errorf("FIFO order broken: %v", p.pendingQueue)
	}
}

// TestChatPage_SendMsg_EmptyOrNilAgentNoop: edge cases must not blow up.
func TestChatPage_SendMsg_EmptyOrNilAgentNoop(t *testing.T) {
	p := NewChatPage(nil)
	p.busy = true
	p2, _ := p.Update(tuitypes.SendMsg{Text: ""})
	if p2.QueueDepth() != 0 {
		t.Error("empty SendMsg must not enqueue")
	}
	// Nil agent: same.
	p.agent = nil
	p2, _ = p.Update(tuitypes.SendMsg{Text: "x"})
	if p2.QueueDepth() != 0 {
		t.Error("nil-agent SendMsg must not enqueue")
	}
}

// TestChatPage_InterruptStream_RestoresLastQueued: spec says the last
// queued message comes back to the editor; earlier queued are discarded.
func TestChatPage_InterruptStream_RestoresLastQueued(t *testing.T) {
	p := NewChatPage(nil)
	p.agent = &agent.Agent{}
	p.busy = true
	cancelled := false
	p.streamCancel = func() { cancelled = true }
	p.pendingQueue = []string{"first", "second", "LATEST"}

	restored, ok := p.InterruptStream()
	if !ok {
		t.Fatal("InterruptStream should report success when busy")
	}
	if restored != "LATEST" {
		t.Errorf("expected LATEST restored, got %q", restored)
	}
	if !cancelled {
		t.Error("stream cancel did not fire on interrupt")
	}
	if p.QueueDepth() != 0 {
		t.Errorf("queue should be drained after interrupt, got %d", p.QueueDepth())
	}
}

// TestChatPage_InterruptStream_NoOpWhenIdle: nothing running + empty
// queue = no-op false return. The TUI checks ok to decide whether to
// toast.
func TestChatPage_InterruptStream_NoOpWhenIdle(t *testing.T) {
	p := NewChatPage(nil)
	if _, ok := p.InterruptStream(); ok {
		t.Error("idle interrupt should return ok=false")
	}
}

// TestChatPage_InterruptStream_BusyEmptyQueue: busy stream with no
// queue → cancel fires, restored is empty, ok=true.
func TestChatPage_InterruptStream_BusyEmptyQueue(t *testing.T) {
	p := NewChatPage(nil)
	p.busy = true
	fired := false
	p.streamCancel = func() { fired = true }
	restored, ok := p.InterruptStream()
	if !ok {
		t.Fatal("busy stream interrupt should report ok")
	}
	if restored != "" {
		t.Errorf("empty queue should restore empty, got %q", restored)
	}
	if !fired {
		t.Error("cancel did not fire")
	}
}

// TestChatPage_QueueDrainOnDone: when the in-flight stream's Done event
// arrives, the next queued message starts streaming. Earlier entries
// stay in the queue (FIFO front consumed).
func TestChatPage_QueueDrainOnDone(t *testing.T) {
	p := NewChatPage(nil)
	p.agent = &agent.Agent{}
	p.busy = true
	p.streaming = true
	p.streamCancel = func() {} // installed so Done branch clears it
	p.pendingQueue = []string{"next-up", "after-that"}

	p2, cmd := p.Update(tuitypes.AgentStreamMsg{Done: true})
	if cmd == nil {
		t.Fatal("Done with non-empty queue must return a cmd to start next stream")
	}
	if p2.QueueDepth() != 1 {
		t.Errorf("expected one item left after drain, got %d", p2.QueueDepth())
	}
	if p2.pendingQueue[0] != "after-that" {
		t.Errorf("queue front advanced incorrectly: %v", p2.pendingQueue)
	}
	if !p2.busy {
		t.Error("draining a queued item must put the page back into busy state")
	}
}

// TestChatPage_QueueDrainOnDone_EmptyQueueLeavesIdle: Done with empty
// queue leaves the page idle. No new stream starts.
func TestChatPage_QueueDrainOnDone_EmptyQueueLeavesIdle(t *testing.T) {
	p := NewChatPage(nil)
	p.busy = true
	p.streaming = true
	p.streamCancel = func() {}
	p2, _ := p.Update(tuitypes.AgentStreamMsg{Done: true})
	if p2.busy {
		t.Error("Done with empty queue should reset busy")
	}
	if p2.QueueDepth() != 0 {
		t.Errorf("empty queue should stay empty, got %d", p2.QueueDepth())
	}
}
