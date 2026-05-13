package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/pipeline"
)

// pipelineRunnerAdapter bridges pipeline.Executor → tools.PipelineRunner.
// internal/tools must not import internal/pipeline (cycle risk + keeps
// tools free of heavy deps), so the wire-up adapter lives here.
type pipelineRunnerAdapter struct {
	exec *pipeline.Executor
}

func (a *pipelineRunnerAdapter) Run(ctx context.Context, request string) (json.RawMessage, error) {
	if a == nil || a.exec == nil {
		return nil, fmt.Errorf("pipeline adapter: executor not configured")
	}
	res, err := a.exec.Run(ctx, request)
	if err != nil {
		return nil, err
	}
	raw, mErr := json.Marshal(res)
	if mErr != nil {
		return nil, fmt.Errorf("pipeline adapter: marshal result: %w", mErr)
	}
	return raw, nil
}
