package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
)

type contractTool struct {
	store  *coordination.Store
	peerID string
}

func NewCoordinationContractTool(store *coordination.Store, peerID string) Tool {
	return &contractTool{store: store, peerID: peerID}
}
func (*contractTool) Name() string { return "agent_contract" }
func (*contractTool) Description() string {
	return "Create, inspect, revise, acknowledge or remove a durable project handoff contract. Contracts carry endpoint/schema versions, acceptance criteria, artifacts, owned paths and pinned dependencies. Identity is bound to this agent. Re-read revisions before update/ack/delete. Readiness means acknowledged current interface dependencies, never verified task completion."
}
func (*contractTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["create","update","get","list","delete","acknowledge","readiness"]},"contract_id":{"type":"string"},"expected_revision":{"type":"integer","minimum":1},"spec":{"type":"object","properties":{"name":{"type":"string"},"endpoint":{"type":"string"},"schema_version":{"type":"string"},"schema":{},"acceptance_criteria":{"type":"array","items":{"type":"string"}},"artifacts":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"path":{"type":"string"},"media_type":{"type":"string"},"digest":{"type":"string"}},"required":["name","path"],"additionalProperties":false}},"workspace":{"type":"string","description":"Your registered canonical workspace; inspect agent_inbox self.workspace"},"worktree":{"type":"string"},"owned_paths":{"type":"array","items":{"type":"string"}},"dependencies":{"type":"array","items":{"type":"object","properties":{"contract_id":{"type":"string"},"revision":{"type":"integer","minimum":1}},"required":["contract_id","revision"],"additionalProperties":false}},"compatibility_changes":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string","enum":["additive","breaking","compatible"]},"from_version":{"type":"string"},"to_version":{"type":"string"},"description":{"type":"string"}},"required":["kind","from_version","to_version","description"],"additionalProperties":false}},"task_id":{"type":"string"}},"required":["name","endpoint","schema_version","schema","acceptance_criteria","workspace","owned_paths"],"additionalProperties":false}},"required":["action"],"additionalProperties":false}`)
}
func (t *contractTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.store == nil || t.peerID == "" {
		return nil, fmt.Errorf("agent_contract: coordination is unavailable")
	}
	var in struct {
		Action   string                     `json:"action"`
		ID       string                     `json:"contract_id"`
		Revision int64                      `json:"expected_revision"`
		Spec     *coordination.ContractSpec `json:"spec"`
	}
	if err := decodeCoordination(input, &in); err != nil {
		return nil, fmt.Errorf("agent_contract: %w", err)
	}
	if in.Action != "create" && in.Action != "list" {
		if err := requireCoordMessageID(in.ID); err != nil {
			return nil, fmt.Errorf("agent_contract: contract_id is required")
		}
	}
	if (in.Action == "update" || in.Action == "acknowledge" || in.Action == "delete") && in.Revision < 1 {
		return nil, fmt.Errorf("agent_contract: expected_revision >= 1 is required")
	}
	if (in.Action == "create" || in.Action == "update") && in.Spec == nil {
		return nil, fmt.Errorf("agent_contract: spec is required")
	}
	var value any
	var err error
	switch in.Action {
	case "create":
		value, err = t.store.CreateContract(ctx, t.peerID, *in.Spec)
	case "update":
		value, err = t.store.UpdateContract(ctx, t.peerID, in.ID, in.Revision, *in.Spec)
	case "get":
		value, err = t.store.ReadContract(ctx, t.peerID, in.ID)
	case "list":
		value, err = t.store.ListContracts(ctx, t.peerID)
	case "acknowledge":
		value, err = t.store.AcknowledgeContract(ctx, t.peerID, in.ID, in.Revision)
	case "readiness":
		value, err = t.store.ContractReadiness(ctx, t.peerID, in.ID)
	case "delete":
		err = t.store.DeleteContract(ctx, t.peerID, in.ID, in.Revision)
		value = map[string]any{"deleted": err == nil, "contract_id": in.ID}
	default:
		return nil, fmt.Errorf("agent_contract: unsupported action %q", in.Action)
	}
	if err != nil {
		return nil, fmt.Errorf("agent_contract: %w", err)
	}
	return json.Marshal(map[string]any{"result": value})
}
