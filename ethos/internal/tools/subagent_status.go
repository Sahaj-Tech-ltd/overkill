package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// SubagentStatusTool returns the live StatusReport for a contract-driven child.
type SubagentStatusTool struct {
	manager *subagent.Manager
}

// NewSubagentStatusTool creates the tool. A nil manager disables it.
func NewSubagentStatusTool(m *subagent.Manager) *SubagentStatusTool {
	return &SubagentStatusTool{manager: m}
}

func (t *SubagentStatusTool) Name() string { return "subagent_status" }

type subagentStatusInput struct {
	ID string `json:"id"`
}

func (t *SubagentStatusTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if t.manager == nil {
		return errorJSON("subagent manager not configured"), nil
	}
	var in subagentStatusInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("subagent_status: invalid input: %w", err)
	}
	if in.ID == "" {
		return errorJSON("id is required"), nil
	}
	st, ok := t.manager.AutonomousStatus(in.ID)
	if !ok {
		return errorJSON(fmt.Sprintf("no contract with id %q", in.ID)), nil
	}
	rep, running, err := t.manager.AutonomousReport(in.ID)
	wrapper := map[string]any{
		"status":  st,
		"running": running,
	}
	if rep != nil {
		wrapper["final_report"] = rep
	}
	if err != nil && rep == nil {
		wrapper["error"] = err.Error()
	}
	out, mErr := json.Marshal(wrapper)
	if mErr != nil {
		return nil, fmt.Errorf("subagent_status: marshal: %w", mErr)
	}
	return out, nil
}
