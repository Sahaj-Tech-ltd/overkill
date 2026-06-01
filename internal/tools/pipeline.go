// Package tools — pipeline_run surfaces the incremental pipeline
// (master plan §4.11) to the agent. The pipeline turns a one-line feature
// request into a 4-stage walk: spec → test → code → refactor.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// PipelineRunner is the minimal surface this tool needs from a real
// pipeline.Executor. Local interface keeps internal/tools free of the
// internal/pipeline import — the wire-up in cmd/overkill plugs a real
// Executor in. Returning a JSON-marshallable shape (passed through
// raw) means the tool doesn't need pipeline.PipelineResult either.
type PipelineRunner interface {
	// Run executes all four stages sequentially. The result is whatever
	// JSON shape the underlying pipeline.PipelineResult marshals to; the
	// tool passes it through to the model without re-shaping.
	Run(ctx context.Context, request string) (json.RawMessage, error)
}

// PipelineTool exposes the runner as a single agent tool. The agent calls
// it with a free-text feature request; the tool returns the full 4-stage
// trace plus a success flag.
type PipelineTool struct {
	runner PipelineRunner
}

func NewPipelineTool(r PipelineRunner) *PipelineTool {
	return &PipelineTool{runner: r}
}

func (t *PipelineTool) Name() string { return "pipeline_run" }

type pipelineRunInput struct {
	// Request is a 1-3 sentence feature description. The pipeline's spec
	// stage will expand it into a full specification before any code is
	// generated.
	Request string `json:"request"`
}

func (t *PipelineTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.runner == nil {
		return errorJSON("pipeline not configured (no provider/model wired in cmd/overkill)"), nil
	}
	var req pipelineRunInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("pipeline_run: %w", err)
	}
	if req.Request == "" {
		return errorJSON("request is required (a 1-3 sentence feature description)"), nil
	}
	raw, err := t.runner.Run(ctx, req.Request)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return raw, nil
}
