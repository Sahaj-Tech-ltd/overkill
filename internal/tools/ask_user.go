// Package tools — `ask_user` tool, lets the agent surface a question to the
// human mid-turn. The actual UI bridge lives in the TUI; this file only
// defines the tool contract.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

// Execute parses the input, calls the bridge, and serializes the answer.
func (t *AskUserTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in AskUserInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("ask_user: parse input: %w", err)
	}
	if in.Question == "" {
		return nil, fmt.Errorf("ask_user: question is required")
	}
	if t.bridge == nil {
		out := AskUserOutput{Cancelled: true}
		return json.Marshal(out)
	}
	text, idx, cancel := t.bridge(ctx, in.Question, in.Choices)
	out := AskUserOutput{Answer: text, Index: idx, Cancelled: cancel}
	return json.Marshal(out)
}
