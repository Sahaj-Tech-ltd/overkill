package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

func TestSetThinkConfig_Toggle(t *testing.T) {
	ta := newTestAgent(nil, nil, nil, nil)

	// Default: disabled
	cfg := ta.ThinkConfig()
	if cfg.Enabled {
		t.Error("default thinkEnabled should be false")
	}

	// Enable
	ta.SetThinkConfig(ThinkConfig{Enabled: true})
	cfg = ta.ThinkConfig()
	if !cfg.Enabled {
		t.Error("thinkEnabled should be true after SetThinkConfig(true)")
	}

	// Disable
	ta.SetThinkConfig(ThinkConfig{Enabled: false})
	cfg = ta.ThinkConfig()
	if cfg.Enabled {
		t.Error("thinkEnabled should be false after SetThinkConfig(false)")
	}
}

func TestThinkEnabled_Method(t *testing.T) {
	ta := newTestAgent(nil, nil, nil, nil)

	if ta.thinkEnabled() {
		t.Error("thinkEnabled() should return false by default")
	}

	ta.SetThinkConfig(ThinkConfig{Enabled: true})
	if !ta.thinkEnabled() {
		t.Error("thinkEnabled() should return true after enabling")
	}

	ta.SetThinkConfig(ThinkConfig{Enabled: false})
	if ta.thinkEnabled() {
		t.Error("thinkEnabled() should return false after disabling")
	}
}

func TestGeneratePreamble(t *testing.T) {
	tests := []struct {
		tool     string
		contains string
	}{
		{"bash", "shell command"},
		{"shell", "shell command"},
		{"read_file", "Reading"},
		{"write_file", "Writing"},
		{"fs_write", "Writing"},
		{"edit_file", "Writing"},
		{"patch", "edit"},
		{"plan_set", "plan"},
		{"plan_create", "plan"},
		{"grep", "Searching"},
		{"search_files", "Searching"},
		{"search_content", "Searching"},
		{"glob", "Searching"},
		{"web_fetch", "web page"},
		{"http_get", "web page"},
		{"memory_search", "memory"},
		{"memory_query", "memory"},
		{"git", "git"},
		{"git_diff", "git"},
		{"git_status", "git"},
		{"browser_open", "browser"},
		{"browser_navigate", "browser"},
		{"browser_screenshot", "screenshot"},
		{"task", "subtask"},
		{"subagent", "subtask"},
		{"delegate", "subtask"},
		{"todo_write", "task list"},
		{"todo", "task list"},
		{"ask_user", "question"},
		{"question", "question"},
		{"ask", "question"},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := generatePreamble(tt.tool)
			if !strings.Contains(strings.ToLower(got), strings.ToLower(tt.contains)) {
				t.Errorf("generatePreamble(%q) = %q, want containing %q", tt.tool, got, tt.contains)
			}
		})
	}
}

func TestGeneratePreamble_Unknown(t *testing.T) {
	got := generatePreamble("unknown_tool_xyz")
	if got != "Working on it..." {
		t.Errorf("generatePreamble(unknown) = %q, want %q", got, "Working on it...")
	}
}

func TestGeneratePreamble_UnderTenWords(t *testing.T) {
	toolNames := []string{
		"bash", "read_file", "write_file", "patch", "plan_set",
		"grep", "web_fetch", "memory_search", "git",
		"browser_open", "browser_screenshot", "task",
		"todo_write", "ask_user", "unknown_tool",
	}
	for _, name := range toolNames {
		preamble := generatePreamble(name)
		words := strings.Fields(preamble)
		if len(words) > 10 {
			t.Errorf("generatePreamble(%q) = %q has %d words, want <= 10", name, preamble, len(words))
		}
	}
}

func TestThinkPreambleEmitted_BeforeToolExecution(t *testing.T) {
	reg := newThinkToolRegistry(t, "read_file", func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"file content"`), nil
	})

	var events []map[string]any
	ta := newTestAgent(nil, reg, nil, nil)
	ta.SetThinkConfig(ThinkConfig{Enabled: true})
	ta.SetEventFn(func(event string, payload map[string]any) {
		events = append(events, map[string]any{
			"event":   event,
			"message": payload["message"],
			"tool":    payload["tool"],
		})
	})

	tc := providers.ToolCall{
		ID:        "call_1",
		Name:      "read_file",
		Arguments: `{"path":"/tmp/test"}`,
	}
	input := json.RawMessage(tc.Arguments)

	// Emit tool_call
	ta.emit("tool_call", map[string]any{
		"tool":  tc.Name,
		"input": string(input),
	})

	// Emit thinking preamble (mirrors react.go)
	if ta.thinkEnabled() {
		ta.emit("thinking", map[string]any{
			"message":    generatePreamble(tc.Name),
			"tool":       tc.Name,
		})
	}

	_, _ = ta.executeTool(t.Context(), tc.Name, input)

	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}

	if events[0]["event"] != "tool_call" {
		t.Errorf("first event = %q, want %q", events[0]["event"], "tool_call")
	}

	if events[1]["event"] != "thinking" {
		t.Errorf("second event = %q, want %q", events[1]["event"], "thinking")
	}
	if events[1]["tool"] != "read_file" {
		t.Errorf("thinking tool = %q, want %q", events[1]["tool"], "read_file")
	}
	if events[1]["message"] != "Reading a file..." {
		t.Errorf("thinking message = %q, want %q", events[1]["message"], "Reading a file...")
	}
}

func TestThinkPreambleNotEmitted_WhenDisabled(t *testing.T) {
	reg := newThinkToolRegistry(t, "bash", func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`"ok"`), nil
	})

	var events []map[string]any
	ta := newTestAgent(nil, reg, nil, nil)
	ta.SetEventFn(func(event string, payload map[string]any) {
		events = append(events, map[string]any{
			"event": event,
		})
	})

	tc := providers.ToolCall{
		ID:        "call_1",
		Name:      "bash",
		Arguments: `{"command":"echo hi"}`,
	}
	input := json.RawMessage(tc.Arguments)

	ta.emit("tool_call", map[string]any{"tool": tc.Name})

	if ta.thinkEnabled() {
		ta.emit("thinking", map[string]any{
			"message": generatePreamble(tc.Name),
			"tool":    tc.Name,
		})
	}

	_, _ = ta.executeTool(t.Context(), tc.Name, input)

	for _, ev := range events {
		if ev["event"] == "thinking" {
			t.Error("thinking event should not be emitted when thinkEnabled is false")
		}
	}
}

func TestSlashThink_Toggle(t *testing.T) {
	ta := newTestAgent(nil, nil, nil, nil)

	// First /think — enables
	resp, consumed := ta.handleSlashCommand(&SlashCommand{Command: "think"})
	if !consumed {
		t.Fatal("expected slash command to be consumed")
	}
	if !ta.ThinkConfig().Enabled {
		t.Error("thinkConfig should be enabled after first /think")
	}
	if !strings.Contains(resp, "Thinking on") {
		t.Errorf("response should indicate thinking on, got: %s", resp)
	}

	// Second /think — disables
	resp, consumed = ta.handleSlashCommand(&SlashCommand{Command: "think"})
	if !consumed {
		t.Fatal("expected slash command to be consumed")
	}
	if ta.ThinkConfig().Enabled {
		t.Error("thinkConfig should be disabled after second /think")
	}
	if !strings.Contains(resp, "Thinking off") {
		t.Errorf("response should indicate thinking off, got: %s", resp)
	}

	// Third /think — enables again
	resp, consumed = ta.handleSlashCommand(&SlashCommand{Command: "think"})
	if !consumed {
		t.Fatal("expected slash command to be consumed")
	}
	if !ta.ThinkConfig().Enabled {
		t.Error("thinkConfig should be enabled after third /think")
	}
}

// newThinkToolRegistry creates a registry with a single mock tool for testing.
func newThinkToolRegistry(t *testing.T, name string, fn func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)) *tools.Registry {
	t.Helper()
	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: name, execute: fn})
	return reg
}
