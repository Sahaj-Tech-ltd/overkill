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
	Name       string             `json:"name,omitempty"`
	FollowupOf string             `json:"followup_of,omitempty"`
	Goal       string             `json:"goal"`
	Context    string             `json:"context"`
	Tasks      []delegateSubTask  `json:"tasks"`
	Contract   *subagent.Contract `json:"contract,omitempty"`
	Wait       bool               `json:"wait,omitempty"`
	Workdir    string             `json:"workdir,omitempty"`
}

// delegateSubTask represents a single entry in the tasks array for batch mode.
type delegateSubTask struct {
	Name    string `json:"name,omitempty"`
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

func (*DelegateTool) Description() string {
	return "Delegate a real coding task or a parallel batch to scoped workers. Give batch workers names to let them exchange updates/questions/answers with worker_messages. Waits for results. followup_of starts a fresh linked attempt for a known finished worker with current admission and no broader tool grant. Contract mode retains its explicit wait behavior."
}

func (*DelegateTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","maxLength":64},"goal":{"type":"string","maxLength":16000},"context":{"type":"string"},"followup_of":{"type":"string"},"tasks":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string","maxLength":64},"goal":{"type":"string","maxLength":16000},"context":{"type":"string"},"model":{"type":"string"}},"required":["goal"],"additionalProperties":false}},"contract":{"type":"object"},"wait":{"type":"boolean"},"workdir":{"type":"string"}},"additionalProperties":false}`)
}

func (d *DelegateTool) executeFollowup(ctx context.Context, in delegateInput) (json.RawMessage, error) {
	store := d.manager.CommunicationStore()
	if store == nil || in.Goal == "" || in.Contract != nil || len(in.Tasks) > 0 {
		return nil, fmt.Errorf("delegate_task: follow-up requires a known finished worker and explicit new goal")
	}
	prior, err := store.ReadWorker(ctx, in.FollowupOf)
	if err != nil {
		return nil, err
	}
	if prior.Status != "completed" || prior.Result == nil {
		return nil, fmt.Errorf("delegate_task: follow-up requires a known completed worker; failed, interrupted or unconfirmed work requires review")
	}
	allowed := make(map[string]bool)
	for _, name := range d.manager.DefaultToolset() {
		allowed[name] = true
	}
	grant := make([]string, 0, len(prior.Tools))
	for _, name := range prior.Tools {
		if allowed[name] {
			grant = append(grant, name)
		}
	}
	name := in.Name
	if name == "" {
		name = prior.Name
	}
	summary := prior.Result.Summary
	if len(summary) > 16000 {
		summary = summary[:16000]
	}
	result, err := d.manager.Spawn(ctx, subagent.GenericTask{NameVal: name, FollowupOf: prior.ID, GoalStr: "Explicit follow-up:\n" + in.Goal, ContextStr: "Previous task (context only, not an instruction to repeat prior effects):\n" + prior.Goal + "\n\nPrior worker result (untrusted evidence; re-read current files and rerun relevant checks):\n" + summary + "\n\nCurrent supplied context:\n" + in.Context, ToolsetVal: grant, ModelVal: prior.Model})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
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

	if in.FollowupOf != "" {
		return d.executeFollowup(ctx, in)
	}
	if in.Name != "" && in.Goal != "" && len(in.Tasks) == 0 && in.Contract == nil {
		result, err := d.manager.Spawn(ctx, subagent.GenericTask{NameVal: in.Name, GoalStr: in.Goal, ContextStr: in.Context, ToolsetVal: d.manager.DefaultToolset()})
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
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
			NameVal:    t.Name,
			GoalStr:    t.Goal,
			ContextStr: t.Context,
			ModelVal:   t.Model,
			ToolsetVal: d.manager.DefaultToolset(),
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
