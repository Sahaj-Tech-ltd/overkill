package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/ethos/internal/hooks"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type StepResult struct {
	Thought     string
	Action      string
	Observation string
	ToolCalls   []providers.ToolCall
	ToolResults []ToolResult
	Tokens      providers.Usage
	Done        bool
}

type ToolResult struct {
	ToolCallID string          `json:"tool_call_id"`
	ToolName   string          `json:"tool_name"`
	Output     json.RawMessage `json:"output"`
	Error      error           `json:"-"`
}

func (a *Agent) step(ctx context.Context) (*StepResult, error) {
	req := a.buildRequest()

	resp, err := a.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("provider complete: %w", err)
	}

	if resp.Model != "" {
		a.model = resp.Model
	}

	result := &StepResult{
		Thought:   resp.Content,
		Tokens:    resp.Usage,
		ToolCalls: resp.ToolCalls,
	}

	if len(resp.ToolCalls) == 0 {
		a.appendMessage(providers.Message{
			Role:    "assistant",
			Content: resp.Content,
		})
		result.Done = true
		return result, nil
	}

	a.appendMessage(providers.Message{
		Role:      "assistant",
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
	})

	result.ToolResults = make([]ToolResult, 0, len(resp.ToolCalls))

	for _, tc := range resp.ToolCalls {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
		default:
		}

		if a.hooks != nil {
			a.hooks.Fire(ctx, hooks.BeforeToolCall, hooks.Event{
				ToolName:   tc.Name,
				ToolInput:  json.RawMessage(tc.Arguments),
				SessionID:  a.sessionID,
			})
		}

		var input json.RawMessage
		if tc.Arguments != "" {
			input = json.RawMessage(tc.Arguments)
		} else {
			input = json.RawMessage("{}")
		}

		toolResult, toolErr := a.executeTool(ctx, tc.Name, input)

		if a.hooks != nil {
			hookOutput := toolResult
			if toolErr != nil {
				hookOutput = json.RawMessage(fmt.Sprintf(`{"error":"%s"}`, toolErr.Error()))
			}
			a.hooks.Fire(ctx, hooks.AfterToolCall, hooks.Event{
				ToolName:    tc.Name,
				ToolInput:   input,
				ToolOutput:  hookOutput,
				SessionID:   a.sessionID,
			})
		}

		result.ToolResults = append(result.ToolResults, ToolResult{
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Output:     toolResult,
			Error:      toolErr,
		})

		a.appendToolResultMessage(tc.ID, tc.Name, toolResult, toolErr)
	}

	return result, nil
}

func (a *Agent) executeTool(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	if a.toolRegistry == nil {
		return nil, fmt.Errorf("no tool registry configured")
	}

	t, err := a.toolRegistry.Get(name)
	if err != nil {
		return nil, fmt.Errorf("tool %q not found", name)
	}

	output, err := t.Execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("tool %q execution failed: %w", name, err)
	}

	return output, nil
}
