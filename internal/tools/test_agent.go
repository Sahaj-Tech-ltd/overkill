// Package tools — Spider-Man problem (master plan §4.12).
//
// `agent_writes_code → agent_writes_tests → agent_says "all good"` is the
// fundamental QA failure mode. The Spider-Man tool spawns an isolated test
// agent that sees the **spec, not the implementation conversation** so its
// verification isn't compromised by knowing how the code was written.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// TestAgentRunner is the minimal surface this tool needs from a real
// agent.TestAgent. Defined locally to avoid an import cycle (agent → tools
// is the canonical direction; tools must not import agent).
type TestAgentRunner interface {
	GenerateTests(ctx context.Context, language string, files []string, spec, description string) (string, error)
	ValidateTests(ctx context.Context, testCode string, implFiles []string) (string, error)
}

// SpiderTestTool wraps a TestAgentRunner. The parent passes a spec + file
// list; the test agent never sees the parent's history.
type SpiderTestTool struct {
	ta TestAgentRunner
}

func NewSpiderTestTool(ta TestAgentRunner) *SpiderTestTool {
	return &SpiderTestTool{ta: ta}
}

func (t *SpiderTestTool) Name() string { return "test_generate" }

type testGenerateInput struct {
	Description string   `json:"description"`
	FilesToTest []string `json:"files_to_test"`
	Spec        string   `json:"spec"`
	Language    string   `json:"language,omitempty"`
}

func (t *SpiderTestTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.ta == nil {
		return errorJSON("test agent not configured"), nil
	}
	var req testGenerateInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("test_generate: %w", err)
	}
	if strings.TrimSpace(req.Spec) == "" {
		return errorJSON("spec is required (Spider-Man enforcement: tests verify the spec, not the implementation)"), nil
	}
	if len(req.FilesToTest) == 0 {
		return errorJSON("files_to_test is required"), nil
	}
	if req.Language == "" {
		req.Language = "auto"
	}
	out, err := t.ta.GenerateTests(ctx, req.Language, req.FilesToTest, req.Spec, req.Description)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	raw, _ := json.Marshal(map[string]any{
		"tests":      out,
		"isolation":  "spec-only — test agent never saw your implementation chat",
		"language":   req.Language,
		"file_count": len(req.FilesToTest),
	})
	return raw, nil
}

// SpiderValidateTool re-runs an existing test bundle against the live impl
// files to catch tests-coupled-to-implementation. Same isolation contract:
// the validator sees only the test code + impl file paths, not the chat.
type SpiderValidateTool struct {
	ta TestAgentRunner
}

func NewSpiderValidateTool(ta TestAgentRunner) *SpiderValidateTool {
	return &SpiderValidateTool{ta: ta}
}

func (t *SpiderValidateTool) Name() string { return "test_validate" }

type testValidateInput struct {
	TestCode  string   `json:"test_code"`
	ImplFiles []string `json:"impl_files"`
}

func (t *SpiderValidateTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.ta == nil {
		return errorJSON("test agent not configured"), nil
	}
	var req testValidateInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("test_validate: %w", err)
	}
	if strings.TrimSpace(req.TestCode) == "" {
		return errorJSON("test_code is required"), nil
	}
	out, err := t.ta.ValidateTests(ctx, req.TestCode, req.ImplFiles)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	raw, _ := json.Marshal(map[string]any{"review": out})
	return raw, nil
}
