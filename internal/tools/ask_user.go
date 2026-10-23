// Package tools — `ask_user` tool, lets the agent surface a question to the
// human mid-turn. The actual UI bridge lives in the TUI; this file only
// defines the tool contract.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// AskUserBridge is the function the tool calls into to actually surface the
// question. The TUI provides this; CLI/headless modes can pass a stub.
type AskUserBridge func(ctx context.Context, prompt string, choices []string) (text string, index int, cancel bool)

// AskUserTool implements Tool. It serializes the question, hands it to the
// bridge, and returns the answer as JSON.
type AskUserTool struct {
	bridge AskUserBridge
}

// AskUserInput is the JSON schema accepted by the tool.
type AskUserInput struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices,omitempty"`
}

// AskUserOutput is the JSON schema returned by the tool.
type AskUserOutput struct {
	Answer    string `json:"answer"`
	Index     int    `json:"index"`
	Cancelled bool   `json:"cancelled"`
}

// NewAskUserTool builds the tool. bridge is required; nil bridge always
// cancels.
func NewAskUserTool(bridge AskUserBridge) *AskUserTool {
	return &AskUserTool{bridge: bridge}
}

// Name returns the tool identifier exposed to the model.
func (t *AskUserTool) Name() string { return "ask_user" }
func (t *AskUserTool) Description() string {
	return "Ask the user a question in this chat, optionally with choices. Waits for their answer or explicit cancellation; stopping the turn cancels the wait."
}
func (t *AskUserTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"question":{"type":"string","minLength":1,"maxLength":8192},"choices":{"type":"array","maxItems":32,"items":{"type":"string","maxLength":1024}}},"required":["question"],"additionalProperties":false}`)
}

// Execute parses the input, calls the bridge, and serializes the answer.
func (t *AskUserTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(input) > 64*1024 {
		return nil, fmt.Errorf("ask_user: input exceeds bound")
	}
	var in AskUserInput
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("ask_user: invalid question input")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("ask_user: one question object required")
	}
	if strings.TrimSpace(in.Question) == "" || len(in.Question) > 8192 || len(in.Choices) > 32 {
		return nil, fmt.Errorf("ask_user: question or choice count exceeds bound")
	}
	for _, choice := range in.Choices {
		if len(choice) > 1024 {
			return nil, fmt.Errorf("ask_user: choice exceeds bound")
		}
	}

	if t.bridge == nil {
		out := AskUserOutput{Cancelled: true}
		return json.Marshal(out)
	}
	text, idx, cancel := t.bridge(ctx, in.Question, in.Choices)
	out := AskUserOutput{Answer: text, Index: idx, Cancelled: cancel}
	return json.Marshal(out)
}
