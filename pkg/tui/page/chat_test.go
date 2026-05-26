package page

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
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

// TestExtractShellMetadata_AgentDrivenShell verifies that EventToolOutput
// from an agent-driven shell call surfaces its metadata line via pump
// → AgentStreamMsg.MetadataLine. This is the Phase 1.5 #8 surface.
func TestExtractShellMetadata_AgentDrivenShell(t *testing.T) {
	out := tools.ShellOutput{ExitCode: 0, Stdout: "hi\n", ElapsedMs: 42, Cwd: "/tmp"}
	raw, _ := json.Marshal(out)
	ev := agent.StreamEvent{
		Type:     agent.EventToolOutput,
		ToolCall: &providers.ToolCall{Name: "shell"},
		Metadata: map[string]interface{}{"output": string(raw)},
	}
	line := extractShellMetadata(ev)
	if !strings.Contains(line, "exit 0") || !strings.Contains(line, "42ms") || !strings.Contains(line, "/tmp") {
		t.Errorf("metadata line missing pieces: %q", line)
	}
}

func TestExtractShellMetadata_NonShellTool(t *testing.T) {
	ev := agent.StreamEvent{
		Type:     agent.EventToolOutput,
		ToolCall: &providers.ToolCall{Name: "fs_read"},
		Metadata: map[string]interface{}{"output": `{"ExitCode":0}`},
	}
	if got := extractShellMetadata(ev); got != "" {
		t.Errorf("non-shell tool should not produce metadata, got %q", got)
	}
}

func TestExtractShellMetadata_MalformedOutput(t *testing.T) {
	ev := agent.StreamEvent{
		Type:     agent.EventToolOutput,
		ToolCall: &providers.ToolCall{Name: "shell"},
		Metadata: map[string]interface{}{"output": "not-json"},
	}
	if got := extractShellMetadata(ev); got != "" {
		t.Errorf("malformed output should silently skip, got %q", got)
	}
}

func TestExtractShellMetadata_MissingToolCall(t *testing.T) {
	ev := agent.StreamEvent{Type: agent.EventToolOutput}
	if got := extractShellMetadata(ev); got != "" {
		t.Errorf("nil ToolCall should skip, got %q", got)
	}
}

// TestChatPage_DollarShell_BypassesAgent: lines starting with `$` are
// routed through the direct shell path, never touching the agent. Even
// works when the agent is nil or busy — the whole point of the bypass.
func TestChatPage_DollarShell_BypassesAgent(t *testing.T) {
	p := NewChatPage(nil) // nil agent intentionally
	p2, cmd := p.Update(tuitypes.SendMsg{Text: "$echo hello"})
	if cmd == nil {
		t.Fatal("$hell SendMsg must return an exec cmd even with nil agent")
	}
	// Run the cmd synchronously to get the result, then feed it back.
	result := cmd()
	r, ok := result.(directShellResultMsg)
	if !ok {
		t.Fatalf("expected directShellResultMsg, got %T", result)
	}
	if r.Err != nil {
		t.Errorf("unexpected exec error: %v", r.Err)
	}
	if r.Command != "echo hello" {
		t.Errorf("expected command 'echo hello', got %q", r.Command)
	}
	if !strings.Contains(r.Output, "hello") {
		t.Errorf("expected output to contain 'hello', got %q", r.Output)
	}
	if !strings.Contains(r.Output, "exit 0") {
		t.Errorf("expected metadata 'exit 0', got %q", r.Output)
	}

	// Feed the result back through Update — should append a tool
	// message under the user message, not crash.
	p3, _ := p2.Update(r)
	if p3.messages.Len() != 2 {
		t.Errorf("expected 2 messages (user + tool), got %d", p3.messages.Len())
	}
}

// TestChatPage_DollarShell_EmptyCommandIgnored: `$` alone is a no-op,
// not an error — the user might be typing `$f` and paused.
func TestChatPage_DollarShell_EmptyCommandIgnored(t *testing.T) {
	p := NewChatPage(nil)
	// startDirectShell returns (page, nil) when command after strip is empty.
	_, cmd := p.startDirectShell("$   ")
	if cmd != nil {
		t.Error("empty $hell command should not spawn exec")
	}
}

// TestChatPage_DollarShell_OutputFormat: the formatted output line one
// must carry the metadata block (exit · elapsed · cwd). This is the
// integration with Phase 1.5 §8.
func TestChatPage_DollarShell_OutputFormat(t *testing.T) {
	got := formatDirectShellOutput(tools.ShellOutput{
		ExitCode:  0,
		Stdout:    "hi\n",
		ElapsedMs: 42,
		Cwd:       "/tmp/x",
	})
	for _, want := range []string{"✓", "exit 0", "42ms", "/tmp/x", "hi"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatDirectShellOutput missing %q in:\n%s", want, got)
		}
	}
}

// TestChatPage_DollarShell_FailedExitMarkedDistinct: non-zero exits get
// a different glyph so the user can scan results at a glance.
func TestChatPage_DollarShell_FailedExitMarkedDistinct(t *testing.T) {
	got := formatDirectShellOutput(tools.ShellOutput{
		ExitCode:  1,
		Stdout:    "",
		ElapsedMs: 100,
	})
	if !strings.Contains(got, "✗") {
		t.Errorf("expected ✗ for non-zero exit, got %q", got)
	}
	if strings.Contains(got, "✓") {
		t.Errorf("✓ should not appear for failed exit, got %q", got)
	}
}

// TestChatPage_DollarShell_ElapsedSecondsFormat: long-running commands
// render seconds (5.2s) not milliseconds for readability.
func TestChatPage_DollarShell_ElapsedSecondsFormat(t *testing.T) {
	got := formatDirectShellOutput(tools.ShellOutput{ElapsedMs: 5200})
	if !strings.Contains(got, "5.2s") {
		t.Errorf("expected '5.2s', got %q", got)
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
