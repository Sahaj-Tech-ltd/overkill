package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// delegateInput models the JSON input accepted by the delegate_task tool.
type delegateInput struct {
	Goal     string             `json:"goal"`
	Context  string             `json:"context"`
	Tasks    []delegateSubTask  `json:"tasks"`
	Contract *subagent.Contract `json:"contract,omitempty"`
	Wait     bool               `json:"wait,omitempty"`
	Workdir  string             `json:"workdir,omitempty"`
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

	// Contract mode: drive an autonomous child against a frozen contract.
	if in.Contract != nil {
		return d.executeContract(ctx, in.Contract, in.Wait, in.Workdir)
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

// executeContract spawns a contract-driven autonomous child via the manager's
// driver factory. When wait=true the call blocks until the child finishes
// and returns the FinalReport; otherwise it returns {id, status:"spawned"}.
func (d *DelegateTool) executeContract(ctx context.Context, c *subagent.Contract, wait bool, workdir string) (json.RawMessage, error) {
	if !d.manager.HasDriverFactory() {
		return errorJSON("contract delegation requires a driver factory; pass goal/context for legacy mode"), nil
	}
	id, err := d.manager.SpawnFromFactory(ctx, c, workdir)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	if !wait {
		out, err := json.Marshal(map[string]any{"id": id, "status": "spawned"})
		if err != nil {
			return nil, fmt.Errorf("delegate_task: marshal: %w", err)
		}
		return out, nil
	}
	rep, err := d.manager.AutonomousWait(ctx, id)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, mErr := json.Marshal(map[string]any{"id": id, "final_report": rep})
	if mErr != nil {
		return nil, fmt.Errorf("delegate_task: marshal: %w", mErr)
	}
	return out, nil
}

// executeSingle spawns a task after attempting decomposition. If the goal
// contains multiple independent items (separated by "- ", "1. ", "also", etc.),
// the Decomposer splits them into parallel sub-agents automatically.
func (d *DelegateTool) executeSingle(ctx context.Context, goal, contextStr string) (json.RawMessage, error) {
	results, err := d.manager.SpawnDecomposed(ctx, goal, contextStr)
	if err != nil {
		return nil, err
	}

	// Single result? Return it directly. Multiple? Return array.
	if len(results) == 1 {
		out, err := json.Marshal(results[0])
		if err != nil {
			return nil, fmt.Errorf("delegate_task: marshal result: %w", err)
		}
		return out, nil
	}

	wrapper := map[string]any{"results": results}
	out, err := json.Marshal(wrapper)
	if err != nil {
		return nil, fmt.Errorf("delegate_task: marshal results: %w", err)
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

	results, err := d.manager.SpawnBatchAutoSplit(ctx, genericTasks)
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
	// json.Marshal on map[string]string is infallible in practice,
	// but we escape the message directly to avoid the panic code path.
	escaped := strings.ReplaceAll(msg, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return json.RawMessage(fmt.Sprintf(`{"error":"%s"}`, escaped))
}
