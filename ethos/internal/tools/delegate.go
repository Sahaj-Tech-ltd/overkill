package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
)

// delegateInput models the JSON input accepted by the delegate_task tool.
type delegateInput struct {
	Goal    string            `json:"goal"`
	Context string            `json:"context"`
	Tasks   []delegateSubTask `json:"tasks"`
}

// delegateSubTask represents a single entry in the tasks array for batch mode.
type delegateSubTask struct {
	Goal    string `json:"goal"`
	Context string `json:"context"`
	Model   string `json:"model"`
}

// DelegateTool delegates work to sub-agents via the subagent.Manager.
type DelegateTool struct {
	manager *subagent.Manager
}

// NewDelegateTool creates a DelegateTool backed by the given Manager.
// If manager is nil the tool is disabled and all calls return an error.
func NewDelegateTool(manager *subagent.Manager) *DelegateTool {
	return &DelegateTool{manager: manager}
}

// Name returns the tool identifier.
func (d *DelegateTool) Name() string {
	return "delegate_task"
}

// Execute dispatches a single task or a batch of tasks to the sub-agent manager.
func (d *DelegateTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in delegateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("delegate_task: invalid input: %w", err)
	}

	// Manager not configured.
	if d.manager == nil {
		return errorJSON("delegation is not configured"), nil
	}

	// Determine mode: batch or single.
	if len(in.Tasks) > 0 {
		return d.executeBatch(ctx, in.Tasks)
	}

	if in.Goal != "" {
		return d.executeSingle(ctx, in.Goal, in.Context)
	}

	return errorJSON("goal is required"), nil
}

// executeSingle spawns a single GenericTask and returns the marshalled Result.
func (d *DelegateTool) executeSingle(ctx context.Context, goal, contextStr string) (json.RawMessage, error) {
	task := subagent.GenericTask{
		GoalStr:    goal,
		ContextStr: contextStr,
	}

	result, err := d.manager.Spawn(ctx, task)
	if err != nil {
		return nil, err
	}

	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("delegate_task: marshal result: %w", err)
	}
	return out, nil
}

// executeBatch spawns a batch of GenericTasks and returns {"results": [...]}.
func (d *DelegateTool) executeBatch(ctx context.Context, tasks []delegateSubTask) (json.RawMessage, error) {
	genericTasks := make([]subagent.Task, len(tasks))
	for i, t := range tasks {
		genericTasks[i] = subagent.GenericTask{
			GoalStr:    t.Goal,
			ContextStr: t.Context,
			ModelVal:   t.Model,
		}
	}

	results, err := d.manager.SpawnBatch(ctx, genericTasks)
	if err != nil {
		return nil, err
	}

	wrapper := map[string]any{"results": results}
	out, err := json.Marshal(wrapper)
	if err != nil {
		return nil, fmt.Errorf("delegate_task: marshal results: %w", err)
	}
	return out, nil
}

// errorJSON returns a JSON object {"error": msg}.
func errorJSON(msg string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return b
}
