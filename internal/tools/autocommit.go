// Package tools — autocommit_stage exposes automation.AutoCommitter so the
// agent can fire a religious commit (master plan §4.8) after a verified
// stage like test-pass / build-green.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
)

// AutocommitStageTool wraps an AutoCommitter for one repo.
type AutocommitStageTool struct {
	committer *automation.AutoCommitter
}

func NewAutocommitStageTool(c *automation.AutoCommitter) *AutocommitStageTool {
	return &AutocommitStageTool{committer: c}
}

func (t *AutocommitStageTool) Name() string { return "autocommit_stage" }

type autocommitInput struct {
	Stage   string `json:"stage"`             // test-pass | build-green | lint-clean | patch-applied
	Summary string `json:"summary,omitempty"` // short reason appended to the commit message
}

func (t *AutocommitStageTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.committer == nil {
		return errorJSON("autocommit not configured"), nil
	}
	var req autocommitInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("autocommit_stage: %w", err)
	}
	if req.Stage == "" {
		return errorJSON("stage is required"), nil
	}
	if req.Summary == "" {
		req.Summary = "agent-driven commit"
	}
	committed, err := t.committer.Commit(ctx, req.Stage, req.Summary)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{
		"stage":     req.Stage,
		"committed": committed,
		"enabled":   t.committer.Enabled(req.Stage),
	})
	return out, nil
}
