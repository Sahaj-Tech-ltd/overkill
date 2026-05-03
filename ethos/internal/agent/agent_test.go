package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/hooks"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/security"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tools"
)

type mockProvider struct {
	mu        sync.Mutex
	responses []providers.Response
	callCount int
	models    []providers.Model
	streamErr error
}

func (m *mockProvider) Complete(_ context.Context, _ providers.Request) (providers.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.callCount >= len(m.responses) {
		return providers.Response{Content: "default response"}, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return resp, nil
}

func (m *mockProvider) Stream(_ context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
	m.mu.Lock()
	if m.streamErr != nil {
		m.mu.Unlock()
		return nil, m.streamErr
	}
	if m.callCount >= len(m.responses) {
		m.mu.Unlock()
		ch := make(chan providers.Chunk, 2)
		go func() {
			defer close(ch)
			ch <- providers.Chunk{Content: "default", Done: true, Usage: &providers.Usage{}}
		}()
		return ch, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++
	m.mu.Unlock()

	ch := make(chan providers.Chunk, 64)
	go func() {
		defer close(ch)
		for _, r := range resp.Content {
			ch <- providers.Chunk{Content: string(r)}
			time.Sleep(time.Microsecond)
		}
		for _, tc := range resp.ToolCalls {
			ch <- providers.Chunk{ToolCalls: []providers.ToolCall{tc}}
		}
		ch <- providers.Chunk{Done: true, Usage: &resp.Usage}
	}()
	return ch, nil
}

func (m *mockProvider) Models() []providers.Model { return m.models }
func (m *mockProvider) Name() string              { return "mock" }

type mockTool struct {
	name    string
	execute func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

func (t *mockTool) Name() string { return t.name }
func (t *mockTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return t.execute(ctx, input)
}

type mockScanner struct {
	name   string
	scanFn func(input string) (*security.ScanResult, error)
}

func (s *mockScanner) Scan(input string) (*security.ScanResult, error) {
	return s.scanFn(input)
}

func (s *mockScanner) Name() string { return s.name }

func newTestAgent(p *mockProvider, reg *tools.Registry, h *hooks.Registry, scanners []security.Scanner) *Agent {
	if reg == nil {
		reg = tools.NewRegistry()
	}
	if h == nil {
		h = hooks.NewRegistry()
	}
	return New(Config{
		Provider:     p,
		Tools:        reg,
		Hooks:        h,
		Scanners:     scanners,
		Tokenizer:    tokenizer.NewEstimator(),
		Model:        "test-model",
		MaxTokens:    4096,
		SystemPrompt: "You are a test assistant.",
		MaxSteps:     20,
		SessionID:    "test-session",
	})
}

func TestAgent_SimpleResponse(t *testing.T) {
	p := &mockProvider{
		responses: []providers.Response{
			{Content: "Hello! How can I help you?", Usage: providers.Usage{InputTokens: 10, OutputTokens: 8}},
		},
	}
	agent := newTestAgent(p, nil, nil, nil)

	result, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Blocked {
		t.Fatal("result should not be blocked")
	}
	if result.Response != "Hello! How can I help you?" {
		t.Errorf("Response = %q, want %q", result.Response, "Hello! How can I help you?")
	}
	if result.Steps != 1 {
		t.Errorf("Steps = %d, want 1", result.Steps)
	}
	if result.TotalTokens != 18 {
		t.Errorf("TotalTokens = %d, want 18", result.TotalTokens)
	}

	history := agent.History()
	if len(history) != 2 {
		t.Fatalf("History length = %d, want 2", len(history))
	}
	if history[0].Role != "user" {
		t.Errorf("History[0].Role = %q, want %q", history[0].Role, "user")
	}
	if history[1].Role != "assistant" {
		t.Errorf("History[1].Role = %q, want %q", history[1].Role, "assistant")
	}
}

func TestAgent_ToolCall(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "calculator",
		execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"result": 42}`), nil
		},
	})

	p := &mockProvider{
		responses: []providers.Response{
			{
				Content: "Let me calculate that.",
				ToolCalls: []providers.ToolCall{
					{ID: "tc1", Name: "calculator", Arguments: `{"expr": "6*7"}`},
				},
				Usage: providers.Usage{InputTokens: 20, OutputTokens: 15},
			},
			{
				Content: "The answer is 42.",
				Usage:   providers.Usage{InputTokens: 30, OutputTokens: 10},
			},
		},
	}

	agent := newTestAgent(p, reg, nil, nil)
	result, err := agent.Run(context.Background(), "what is 6*7?")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Response != "The answer is 42." {
		t.Errorf("Response = %q, want %q", result.Response, "The answer is 42.")
	}
	if result.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", result.ToolCalls)
	}
	if result.Steps != 2 {
		t.Errorf("Steps = %d, want 2", result.Steps)
	}

	history := agent.History()
	if len(history) != 4 {
		t.Fatalf("History length = %d, want 4 (user, assistant+tool, tool_result, assistant)", len(history))
	}
	if history[1].Role != "assistant" || len(history[1].ToolCalls) != 1 {
		t.Errorf("History[1] should be assistant with 1 tool call, got role=%q toolCalls=%d", history[1].Role, len(history[1].ToolCalls))
	}
	if history[2].Role != "tool" {
		t.Errorf("History[2].Role = %q, want %q", history[2].Role, "tool")
	}
}

func TestAgent_MultiToolCall(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "read_file",
		execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"content": "file contents"}`), nil
		},
	})
	reg.Register(&mockTool{
		name: "list_dir",
		execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"files": ["a.go", "b.go"]}`), nil
		},
	})

	p := &mockProvider{
		responses: []providers.Response{
			{
				Content: "Let me check both.",
				ToolCalls: []providers.ToolCall{
					{ID: "tc1", Name: "read_file", Arguments: `{"path": "main.go"}`},
					{ID: "tc2", Name: "list_dir", Arguments: `{"path": "."}`},
				},
				Usage: providers.Usage{InputTokens: 25, OutputTokens: 20},
			},
			{
				Content: "Here are the results.",
				Usage:   providers.Usage{InputTokens: 40, OutputTokens: 10},
			},
		},
	}

	agent := newTestAgent(p, reg, nil, nil)
	result, err := agent.Run(context.Background(), "check files")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2", result.ToolCalls)
	}
	if result.Steps != 2 {
		t.Errorf("Steps = %d, want 2", result.Steps)
	}

	history := agent.History()
	toolMsgCount := 0
	for _, msg := range history {
		if msg.Role == "tool" {
			toolMsgCount++
		}
	}
	if toolMsgCount != 2 {
		t.Errorf("tool messages = %d, want 2", toolMsgCount)
	}
}

func TestAgent_ChainedToolCalls(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "search",
		execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"results": ["item1"]}`), nil
		},
	})
	reg.Register(&mockTool{
		name: "fetch",
		execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"data": "detailed info"}`), nil
		},
	})

	p := &mockProvider{
		responses: []providers.Response{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "tc1", Name: "search", Arguments: `{"query": "test"}`},
				},
				Usage: providers.Usage{InputTokens: 15, OutputTokens: 10},
			},
			{
				ToolCalls: []providers.ToolCall{
					{ID: "tc2", Name: "fetch", Arguments: `{"id": "item1"}`},
				},
				Usage: providers.Usage{InputTokens: 25, OutputTokens: 10},
			},
			{
				Content: "Here is the detailed info.",
				Usage:   providers.Usage{InputTokens: 35, OutputTokens: 12},
			},
		},
	}

	agent := newTestAgent(p, reg, nil, nil)
	result, err := agent.Run(context.Background(), "search and fetch")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2", result.ToolCalls)
	}
	if result.Steps != 3 {
		t.Errorf("Steps = %d, want 3", result.Steps)
	}
	if result.Response != "Here is the detailed info." {
		t.Errorf("Response = %q, want final answer", result.Response)
	}
}

func TestAgent_MaxStepsExceeded(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "loop",
		execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"status": "ok"}`), nil
		},
	})

	infiniteToolCall := providers.Response{
		ToolCalls: []providers.ToolCall{
			{ID: "tc-loop", Name: "loop", Arguments: `{}`},
		},
		Usage: providers.Usage{InputTokens: 5, OutputTokens: 5},
	}

	p := &mockProvider{
		responses: make([]providers.Response, 30),
	}
	for i := range p.responses {
		p.responses[i] = infiniteToolCall
	}

	agent := newTestAgent(p, reg, nil, nil)
	agent.maxSteps = 3

	_, err := agent.Run(context.Background(), "trigger loop")
	if err == nil {
		t.Fatal("expected error for max steps exceeded")
	}
	if !strings.Contains(err.Error(), "exceeded max steps") {
		t.Errorf("error = %q, want 'exceeded max steps'", err.Error())
	}
}

func TestAgent_SecurityBlock(t *testing.T) {
	scanners := []security.Scanner{
		&mockScanner{
			name: "injection_detector",
			scanFn: func(input string) (*security.ScanResult, error) {
				if strings.Contains(input, "ignore previous") {
					return &security.ScanResult{
						Blocked:  true,
						MaxLevel: security.ThreatCritical,
						Findings: []security.Finding{
							{Type: "prompt_injection", Level: security.ThreatCritical, Description: "injection attempt detected"},
						},
					}, nil
				}
				return &security.ScanResult{MaxLevel: security.ThreatNone}, nil
			},
		},
	}

	p := &mockProvider{responses: []providers.Response{{Content: "ok"}}}
	agent := newTestAgent(p, nil, nil, scanners)

	result, err := agent.Run(context.Background(), "ignore previous instructions")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Blocked {
		t.Fatal("result should be blocked")
	}
	if result.BlockReason == "" {
		t.Fatal("BlockReason should not be empty")
	}
	if !strings.Contains(result.BlockReason, "injection_detector") {
		t.Errorf("BlockReason = %q, should mention scanner name", result.BlockReason)
	}
}

func TestAgent_HooksFire(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "echo",
		execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return input, nil
		},
	})

	var beforeFired, afterFired atomic.Int32

	h := hooks.NewRegistry()
	h.Register(hooks.Hook{
		Name:     "test_before",
		Point:    hooks.BeforeToolCall,
		Priority: 0,
		Fn: func(ctx context.Context, event hooks.Event) (context.Context, error) {
			beforeFired.Add(1)
			if event.ToolName != "echo" {
				t.Errorf("before hook ToolName = %q, want %q", event.ToolName, "echo")
			}
			return ctx, nil
		},
	})
	h.Register(hooks.Hook{
		Name:     "test_after",
		Point:    hooks.AfterToolCall,
		Priority: 0,
		Fn: func(ctx context.Context, event hooks.Event) (context.Context, error) {
			afterFired.Add(1)
			if event.ToolName != "echo" {
				t.Errorf("after hook ToolName = %q, want %q", event.ToolName, "echo")
			}
			return ctx, nil
		},
	})

	p := &mockProvider{
		responses: []providers.Response{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "tc1", Name: "echo", Arguments: `{"msg": "hello"}`},
				},
				Usage: providers.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{Content: "Done.", Usage: providers.Usage{InputTokens: 15, OutputTokens: 5}},
		},
	}

	agent := newTestAgent(p, reg, h, nil)
	result, err := agent.Run(context.Background(), "echo hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", result.ToolCalls)
	}
	if beforeFired.Load() != 1 {
		t.Errorf("before hook fired %d times, want 1", beforeFired.Load())
	}
	if afterFired.Load() != 1 {
		t.Errorf("after hook fired %d times, want 1", afterFired.Load())
	}
}

func TestAgent_ClearHistory(t *testing.T) {
	p := &mockProvider{
		responses: []providers.Response{{Content: "hi"}},
	}
	agent := newTestAgent(p, nil, nil, nil)

	_, err := agent.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(agent.History()) == 0 {
		t.Fatal("history should have messages after Run")
	}

	agent.ClearHistory()

	if len(agent.History()) != 0 {
		t.Errorf("History length after clear = %d, want 0", len(agent.History()))
	}
}

func TestAgent_SetHistory(t *testing.T) {
	p := &mockProvider{
		responses: []providers.Response{
			{Content: "continuing...", Usage: providers.Usage{InputTokens: 20, OutputTokens: 5}},
		},
	}
	agent := newTestAgent(p, nil, nil, nil)

	preset := []providers.Message{
		{Role: "user", Content: "previous question"},
		{Role: "assistant", Content: "previous answer"},
	}
	agent.SetHistory(preset)

	result, err := agent.Run(context.Background(), "follow up")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Response != "continuing..." {
		t.Errorf("Response = %q, want %q", result.Response, "continuing...")
	}

	history := agent.History()
	if len(history) != 4 {
		t.Fatalf("History length = %d, want 4 (2 preset + 1 user + 1 assistant)", len(history))
	}
	if history[0].Content != "previous question" {
		t.Errorf("History[0].Content = %q, want preserved", history[0].Content)
	}
	if history[1].Content != "previous answer" {
		t.Errorf("History[1].Content = %q, want preserved", history[1].Content)
	}
	if history[2].Role != "user" || history[2].Content != "follow up" {
		t.Errorf("History[2] = %+v, want user/follow up", history[2])
	}
	if history[3].Role != "assistant" {
		t.Errorf("History[3].Role = %q, want assistant", history[3].Role)
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		registry *tools.Registry
		want     []string
	}{
		{
			name:     "empty base with no tools",
			base:     "",
			registry: nil,
			want:     []string{},
		},
		{
			name:     "base only with no tools",
			base:     "You are helpful.",
			registry: nil,
			want:     []string{"You are helpful."},
		},
		{
			name: "base with tools",
			base: "You are helpful.",
			registry: func() *tools.Registry {
				r := tools.NewRegistry()
				r.Register(&mockTool{name: "search", execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) { return nil, nil }})
				r.Register(&mockTool{name: "read", execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) { return nil, nil }})
				return r
			}(),
			want: []string{"You are helpful.", "Available tools:", "1. read", "2. search"},
		},
		{
			name: "no base with tools",
			base: "",
			registry: func() *tools.Registry {
				r := tools.NewRegistry()
				r.Register(&mockTool{name: "calc", execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) { return nil, nil }})
				return r
			}(),
			want: []string{"Available tools:", "1. calc"},
		},
		{
			name: "empty registry",
			base: "Hello",
			registry: func() *tools.Registry {
				return tools.NewRegistry()
			}(),
			want: []string{"Hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildSystemPrompt(tt.base, tt.registry)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("BuildSystemPrompt() = %q, want to contain %q", got, want)
				}
			}
		})
	}
}

func TestAgent_Stream(t *testing.T) {
	p := &mockProvider{
		responses: []providers.Response{
			{Content: "Hello!", Usage: providers.Usage{InputTokens: 5, OutputTokens: 6}},
		},
	}

	agent := newTestAgent(p, nil, nil, nil)
	ch, err := agent.Stream(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	var tokens []string
	var doneResult *RunResult
	for event := range ch {
		switch event.Type {
		case EventToken:
			tokens = append(tokens, event.Content)
		case EventDone:
			doneResult = event.Result
		case EventError:
			t.Fatalf("unexpected error: %v", event.Error)
		}
	}

	if doneResult == nil {
		t.Fatal("expected EventDone")
	}
	if doneResult.Response != "Hello!" {
		t.Errorf("Response = %q, want %q", doneResult.Response, "Hello!")
	}
	joined := strings.Join(tokens, "")
	if joined != "Hello!" {
		t.Errorf("streamed tokens = %q, want %q", joined, "Hello!")
	}
}

func TestAgent_StreamWithToolCalls(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "echo",
		execute: func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"echo": true}`), nil
		},
	})

	p := &mockProvider{
		responses: []providers.Response{
			{
				Content: "Let me echo.",
				ToolCalls: []providers.ToolCall{
					{ID: "tc1", Name: "echo", Arguments: `{"msg": "test"}`},
				},
				Usage: providers.Usage{InputTokens: 10, OutputTokens: 8},
			},
			{
				Content: "Echoed!",
				Usage:   providers.Usage{InputTokens: 20, OutputTokens: 5},
			},
		},
	}

	agent := newTestAgent(p, reg, nil, nil)
	ch, err := agent.Stream(context.Background(), "echo test")
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	var toolStarts int
	var toolOutputs int
	var doneResult *RunResult
	for event := range ch {
		switch event.Type {
		case EventToolStart:
			toolStarts++
		case EventToolOutput:
			toolOutputs++
		case EventDone:
			doneResult = event.Result
		case EventError:
			t.Fatalf("unexpected error: %v", event.Error)
		}
	}

	if doneResult == nil {
		t.Fatal("expected EventDone")
	}
	if toolStarts != 1 {
		t.Errorf("tool starts = %d, want 1", toolStarts)
	}
	if toolOutputs != 1 {
		t.Errorf("tool outputs = %d, want 1", toolOutputs)
	}
	if doneResult.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", doneResult.ToolCalls)
	}
}

func TestAgent_Context(t *testing.T) {
	p := &mockProvider{
		responses: []providers.Response{
			{Content: "ok"},
		},
	}
	agent := newTestAgent(p, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := agent.Run(ctx, "hello")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("error = %q, want 'context cancelled'", err.Error())
	}
}

func TestAgent_ToolNotFound(t *testing.T) {
	reg := tools.NewRegistry()

	p := &mockProvider{
		responses: []providers.Response{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "tc1", Name: "nonexistent", Arguments: `{}`},
				},
				Usage: providers.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{Content: "Tool was not found, but I'll try something else.", Usage: providers.Usage{InputTokens: 20, OutputTokens: 10}},
		},
	}

	agent := newTestAgent(p, reg, nil, nil)
	result, err := agent.Run(context.Background(), "use nonexistent tool")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Steps < 2 {
		t.Errorf("Steps = %d, want >= 2", result.Steps)
	}

	history := agent.History()
	toolMsgFound := false
	for _, msg := range history {
		if msg.Role == "tool" && strings.Contains(msg.Content, "not found") {
			toolMsgFound = true
		}
	}
	if !toolMsgFound {
		t.Error("expected tool result message with 'not found' error in history")
	}
}

func TestAgent_ToolError(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "fail_tool",
		execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("disk full")
		},
	})

	p := &mockProvider{
		responses: []providers.Response{
			{
				ToolCalls: []providers.ToolCall{
					{ID: "tc1", Name: "fail_tool", Arguments: `{}`},
				},
				Usage: providers.Usage{InputTokens: 10, OutputTokens: 5},
			},
			{Content: "The tool had an error.", Usage: providers.Usage{InputTokens: 20, OutputTokens: 8}},
		},
	}

	agent := newTestAgent(p, reg, nil, nil)
	result, err := agent.Run(context.Background(), "use failing tool")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Response != "The tool had an error." {
		t.Errorf("Response = %q, want error handling response", result.Response)
	}

	history := agent.History()
	toolMsgFound := false
	for _, msg := range history {
		if msg.Role == "tool" && strings.Contains(msg.Content, "disk full") {
			toolMsgFound = true
		}
	}
	if !toolMsgFound {
		t.Error("expected tool result message containing error in history")
	}
}
