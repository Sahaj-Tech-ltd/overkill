package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
)

// SubagentWaitTool blocks the caller until the named contract child finishes
// (or the optional timeout elapses) and returns the FinalReport.
type SubagentWaitTool struct {
	manager *subagent.Manager
}

// NewSubagentWaitTool creates the tool. A nil manager disables it.
func NewSubagentWaitTool(m *subagent.Manager) *SubagentWaitTool {
	return &SubagentWaitTool{manager: m}
}

func (t *SubagentWaitTool) Name() string { return "subagent_wait" }

type subagentWaitInput struct {
	ID             string `json:"id"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func (t *SubagentWaitTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if t.manager == nil {
		return errorJSON("subagent manager not configured"), nil
	}
	var in subagentWaitInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("subagent_wait: invalid input: %w", err)
	}
	if in.ID == "" {
		return errorJSON("id is required"), nil
	}

	wctx := ctx
	if in.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		wctx, cancel = context.WithTimeout(ctx, time.Duration(in.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	rep, err := t.manager.AutonomousWait(wctx, in.ID)
	if err != nil {
		// Distinguish timeout vs not-found.
		if wctx.Err() != nil {
			st, ok := t.manager.AutonomousStatus(in.ID)
			out := map[string]any{
				"timeout": true,
				"error":   err.Error(),
			}
			if ok {
				out["status"] = st
			}
			b, _ := json.Marshal(out)
			return b, nil
		}
		return errorJSON(err.Error()), nil
	}
	b, mErr := json.Marshal(map[string]any{"final_report": rep})
	if mErr != nil {
		return nil, fmt.Errorf("subagent_wait: marshal: %w", mErr)
	}
	return b, nil
}
