package tools

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
)

// NewRemoteCoordinationTools constructs six tools for one explicitly provisioned
// remote peer identity. Endpoint and scoped token belong to trusted application
// setup. Callers must not replace local identity-bound tools implicitly. This
// constructor alone does not persist a session binding or install an executor.
func NewRemoteCoordinationTools(endpoint, token string) ([]Tool, error) {
	if _, err := coordination.NewPeerHTTPClient(endpoint); err != nil {
		return nil, err
	}
	if raw, err := hex.DecodeString(token); err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("scoped peer credential required")
	}
	var result []Tool
	for _, tool := range NewCoordinationTools(nil, "") {
		spec := tool.(*coordinationTool)
		if spec.Name() == "agent_task_graph" {
			continue
		}
		schema := spec.Schema()
		if spec.Name() == "agent_inbox" {
			var v map[string]any
			_ = json.Unmarshal(schema, &v)
			v["properties"] = map[string]any{"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}}
			schema, _ = json.Marshal(v)
		}
		if spec.Name() == "agent_await" {
			var v map[string]any
			_ = json.Unmarshal(schema, &v)
			v["properties"].(map[string]any)["wait_seconds"] = map[string]any{"type": "number", "minimum": 0, "maximum": 25, "default": 25}
			schema, _ = json.Marshal(v)
		}
		description := "Scoped ACP transport: " + spec.Description()
		if spec.Name() == "agent_await" {
			description = "Scoped ACP transport: wait up to 25 seconds for durable state to advance or become terminal. Ending observation does not cancel work."
		}
		result = append(result, &remoteCoordinationTool{name: spec.Name(), description: description, schema: schema, endpoint: endpoint, token: token})
	}
	return result, nil
}

type remoteCoordinationTool struct {
	name, description, endpoint, token string
	expected                           *coordination.Peer
	schema                             json.RawMessage
}

func (t *remoteCoordinationTool) Name() string        { return t.name }
func (t *remoteCoordinationTool) Description() string { return t.description }
func (t *remoteCoordinationTool) Schema() json.RawMessage {
	return append(json.RawMessage(nil), t.schema...)
}
func (t *remoteCoordinationTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.expected != nil {
		current, err := acp.NewClient(t.endpoint, t.token).Coordination(ctx, "peers", acp.CoordinationRequest{})
		if err != nil {
			return nil, err
		}
		if current.Self == nil || current.Self.ID != t.expected.ID || current.Self.ProjectID != t.expected.ProjectID || current.Self.Workspace != t.expected.Workspace {
			return nil, coordination.ErrUnauthorized
		}
	}
	var in acp.CoordinationRequest
	operation := ""
	switch t.name {
	case "agent_send":
		var send coordination.SendInput
		if err := decodeCoordination(raw, &send); err != nil {
			return nil, err
		}
		in.Send = &send
		operation = "send"
	case "agent_reply":
		var v struct {
			MessageID string          `json:"message_id"`
			Payload   json.RawMessage `json:"payload"`
			Key       string          `json:"idempotency_key"`
		}
		if err := decodeCoordination(raw, &v); err != nil {
			return nil, err
		}
		in.MessageID = v.MessageID
		in.Payload = v.Payload
		in.IdempotencyKey = v.Key
		operation = "reply"
	case "agent_inbox":
		var v struct {
			Limit int `json:"limit"`
		}
		if err := decodeCoordination(raw, &v); err != nil {
			return nil, err
		}
		in.Limit = v.Limit
		operation = "inbox"
	case "agent_task_status":
		var v struct {
			ID    string `json:"message_id"`
			After int64  `json:"after_revision"`
			Limit int    `json:"limit"`
		}
		if err := decodeCoordination(raw, &v); err != nil {
			return nil, err
		}
		in.MessageID = v.ID
		in.AfterRevision = v.After
		in.Limit = v.Limit
		operation = "status"
	case "agent_await":
		var v struct {
			ID      string   `json:"message_id"`
			After   *int64   `json:"after_revision"`
			Seconds *float64 `json:"wait_seconds"`
		}
		if err := decodeCoordination(raw, &v); err != nil {
			return nil, err
		}
		seconds := 25.0
		if v.Seconds != nil {
			seconds = *v.Seconds
		}
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > 25 {
			return nil, fmt.Errorf("wait_seconds must be0..25")
		}
		in.MessageID = v.ID
		in.WaitMilliseconds = int(seconds * 1000)
		operation = "await"
		if v.After != nil {
			in.AfterRevision = *v.After
		} else {
			current, err := acp.NewClient(t.endpoint, t.token).Coordination(ctx, "status", acp.CoordinationRequest{MessageID: v.ID})
			if err != nil {
				return nil, err
			}
			if current.Message == nil {
				return nil, fmt.Errorf("missing durable message")
			}
			in.AfterRevision = current.Message.Revision
		}
	case "agent_cancel":
		var v struct {
			ID       string          `json:"message_id"`
			Revision int64           `json:"expected_revision"`
			Payload  json.RawMessage `json:"payload"`
		}
		if err := decodeCoordination(raw, &v); err != nil {
			return nil, err
		}
		in.MessageID = v.ID
		in.ExpectedRevision = v.Revision
		in.Payload = v.Payload
		operation = "cancel"
		if len(in.Payload) == 0 {
			in.Payload = json.RawMessage(`{}`)
		}
		if in.ExpectedRevision == 0 {
			current, err := acp.NewClient(t.endpoint, t.token).Coordination(ctx, "status", acp.CoordinationRequest{MessageID: v.ID})
			if err != nil {
				return nil, err
			}
			if current.Message == nil {
				return nil, fmt.Errorf("missing durable message")
			}
			in.ExpectedRevision = current.Message.Revision
		}
	}
	if operation == "send" || operation == "reply" {
		in.Execution = coordination.ExecutionTransportProof(ctx)
	}
	out, err := acp.NewClient(t.endpoint, t.token).Coordination(ctx, operation, in)
	if err != nil {
		return nil, err
	}
	if operation == "inbox" {
		peers, err := acp.NewClient(t.endpoint, t.token).Coordination(ctx, "peers", acp.CoordinationRequest{})
		if err != nil {
			return nil, err
		}
		out.Peers = peers.Peers
	}
	return json.Marshal(out)
}

// NewBoundRemoteCoordinationTools requires the application's persisted identity
// on every call before sending effects. The model cannot supply this snapshot.
func NewBoundRemoteCoordinationTools(endpoint, token string, expected coordination.Peer) ([]Tool, error) {
	result, err := NewRemoteCoordinationTools(endpoint, token)
	if err != nil {
		return nil, err
	}
	for _, tool := range result {
		copy := expected
		tool.(*remoteCoordinationTool).expected = &copy
	}
	return result, nil
}
