package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
)

const coordinationPayloadLimit = 64 * 1024

// NewCoordinationTools binds identity outside model input. The caller supplies
// an authenticated peer belonging to the session's project and owns the store.
func NewCoordinationTools(store *coordination.Store, peerID string) []Tool {
	specs := []struct {
		name, description string
		required          []string
		properties        map[string]any
	}{
		{"agent_send", "Persist a message to a named peer in this project and return its durable message handle immediately. An accepted receipt does not mean work is completed. Reuse the same idempotency_key when retrying the identical request.", []string{"to", "kind", "payload", "idempotency_key"}, map[string]any{
			"to":      coordString("Recipient peer name in this project"),
			"kind":    map[string]any{"type": "string", "enum": []string{"request", "answer", "progress", "blocker", "contract_changed", "completion"}},
			"payload": coordPayload(), "idempotency_key": coordKey(),
			"conversation_id": coordString("Optional existing conversation UUID"), "task_id": coordString("Optional task correlation ID"),
			"required_contracts": map[string]any{"type": "array", "maxItems": 32, "description": "Immutable agreement pins. Recipient must acknowledge the current pinned revision and recursive dependencies before execution. Stale/deleted pins fail the request; agreement never proves tests passed.", "items": map[string]any{"type": "object", "properties": map[string]any{"contract_id": map[string]any{"type": "string", "format": "uuid"}, "revision": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"contract_id", "revision"}, "additionalProperties": false}},
			"depends_on":         map[string]any{"type": "array", "maxItems": 32, "uniqueItems": true, "items": map[string]any{"type": "string", "format": "uuid"}, "description": "Existing request IDs involving this sender. Work waits until all complete; failed/cancelled prerequisites fail this request. Immutable after send."},
			"in_reply_to":        coordString("Optional received message ID; agent_reply resolves its sender automatically"),
			"deadline":           map[string]any{"type": "string", "format": "date-time", "description": "Optional delivery/work deadline in RFC3339 format"},
		}},
		{"agent_reply", "Persist an answer to a received message. The store resolves its original sender within the authenticated project; no sender, token, or destination identity is accepted.", []string{"message_id", "payload", "idempotency_key"}, map[string]any{
			"message_id": coordString("Received message ID"), "payload": coordPayload(), "idempotency_key": coordKey(),
		}},
		{"agent_inbox", "Discover this agent's identity and the named peers/roles in its project, and read pending, started and blocked messages addressed to it. Reading does not claim or execute work. No credentials are returned.", nil, map[string]any{
			"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50},
			"after_escalation": map[string]any{"type": "integer", "minimum": 0, "default": 0},
		}},
		{"agent_task_graph", "Inspect this request and its prerequisite graph. Readiness reports dependency eligibility, not provider health or execution admission. Only participant-visible messages are returned; hidden prerequisites and truncated traversal are explicit. Dependencies are immutable.", []string{"message_id"}, map[string]any{"message_id": coordString("Root request ID"), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 100}}},
		{"agent_task_status", "Read durable state and bounded recorded progress/result events for a message involving this peer. Resume event replay with next_revision; has_more indicates another page. A message outside this correspondence is not accessible.", []string{"message_id"}, map[string]any{"message_id": coordString("Message ID"), "after_revision": map[string]any{"type": "integer", "minimum": 0, "default": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50}}},
		{"agent_await", "Wait up to 30 seconds for a message revision to advance or work to become terminal. Returns current message and timed_out=true for this bounded wait; caller cancellation remains an error. Omitting after_revision waits from the current revision.", []string{"message_id"}, map[string]any{
			"message_id": coordString("Message ID"), "after_revision": map[string]any{"type": "integer", "minimum": 0},
			"wait_seconds": map[string]any{"type": "number", "minimum": 0, "maximum": 30, "default": 30},
		}},
		{"agent_cancel", "Request a durable cancellation using an optimistic revision check. A completed task or conflicting revision returns an error; accepted cancellation does not undo earlier side effects.", []string{"message_id"}, map[string]any{
			"message_id": coordString("Message ID"), "expected_revision": map[string]any{"type": "integer", "minimum": 1}, "payload": coordPayload(),
		}},
	}
	result := make([]Tool, 0, len(specs))
	for _, spec := range specs {
		required := spec.required
		if required == nil {
			required = []string{}
		}
		schema, _ := json.Marshal(map[string]any{"type": "object", "properties": spec.properties, "required": required, "additionalProperties": false})
		result = append(result, &coordinationTool{store: store, peerID: peerID, name: spec.name, description: spec.description, schema: schema})
	}
	return result
}

func coordString(description string) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "description": description}
}
func coordKey() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable retry key for this exact request"}
}
func coordPayload() map[string]any {
	return map[string]any{"description": "JSON payload, at most 64 KiB when serialized"}
}

type coordinationTool struct {
	store                     *coordination.Store
	peerID, name, description string
	schema                    json.RawMessage
}

func (t *coordinationTool) Name() string            { return t.name }
func (t *coordinationTool) Description() string     { return t.description }
func (t *coordinationTool) Schema() json.RawMessage { return append(json.RawMessage(nil), t.schema...) }

func decodeCoordination(input json.RawMessage, target any) error {
	data := bytes.TrimSpace(input)
	if len(data) == 0 || data[0] != '{' || len(data) > 72*1024 {
		return fmt.Errorf("bounded JSON object input is required")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name, value := range fields {
		if name != "payload" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("%s cannot be null", name)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("exactly one JSON object is required")
	}
	return nil
}
func validateCoordPayload(payload json.RawMessage) error {
	if len(payload) == 0 || len(payload) > coordinationPayloadLimit || !json.Valid(payload) {
		return fmt.Errorf("JSON payload of at most 64 KiB is required")
	}
	return nil
}
func validateCoordKey(key string) error {
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return fmt.Errorf("idempotency_key of 1..128 bytes is required")
	}
	return nil
}
func requireCoordMessageID(messageID string) error {
	if strings.TrimSpace(messageID) == "" || len(messageID) > 128 {
		return fmt.Errorf("message_id of 1..128 bytes is required")
	}
	return nil
}
func coordinationMessageOutput(message coordination.Message) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"message": message})
}

func (t *coordinationTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.store == nil || t.peerID == "" {
		return nil, fmt.Errorf("%s: coordination is not configured", t.name)
	}
	result, err := t.execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", t.name, err)
	}
	return result, nil
}
func (t *coordinationTool) execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	switch t.name {
	case "agent_send":
		var in coordination.SendInput
		if err := decodeCoordination(input, &in); err != nil {
			return nil, err
		}
		if err := validateCoordPayload(in.Payload); err != nil {
			return nil, err
		}
		if err := validateCoordKey(in.IdempotencyKey); err != nil {
			return nil, err
		}
		message, err := t.store.Send(ctx, t.peerID, in)
		if err != nil {
			return nil, err
		}
		return coordinationMessageOutput(message)
	case "agent_reply":
		var in struct {
			MessageID string          `json:"message_id"`
			Payload   json.RawMessage `json:"payload"`
			Key       string          `json:"idempotency_key"`
		}
		if err := decodeCoordination(input, &in); err != nil {
			return nil, err
		}
		if err := requireCoordMessageID(in.MessageID); err != nil {
			return nil, err
		}
		if err := validateCoordPayload(in.Payload); err != nil {
			return nil, err
		}
		if err := validateCoordKey(in.Key); err != nil {
			return nil, err
		}
		message, err := t.store.Reply(ctx, t.peerID, in.MessageID, in.Payload, in.Key)
		if err != nil {
			return nil, err
		}
		return coordinationMessageOutput(message)
	case "agent_inbox":
		var in struct {
			Limit           *int  `json:"limit"`
			AfterEscalation int64 `json:"after_escalation"`
		}
		if err := decodeCoordination(input, &in); err != nil {
			return nil, err
		}
		limit := 50
		if in.Limit != nil {
			limit = *in.Limit
		}
		if limit < 1 || limit > 200 {
			return nil, fmt.Errorf("limit must be 1..200")
		}
		messages, err := t.store.Inbox(ctx, t.peerID, limit)
		if err != nil {
			return nil, err
		}
		peers, err := t.store.Peers(ctx, t.peerID)
		if err != nil {
			return nil, err
		}
		var self *coordination.Peer
		for _, peer := range peers {
			if peer.ID == t.peerID {
				copy := peer
				self = &copy
			}
		}
		if self == nil {
			return nil, coordination.ErrUnauthorized
		}
		escalations, err := t.store.ReadEscalations(ctx, t.peerID, in.AfterEscalation, limit+1)
		if err != nil {
			return nil, err
		}
		more := len(escalations) > limit
		if more {
			escalations = escalations[:limit]
		}
		cursor := in.AfterEscalation
		if len(escalations) > 0 {
			cursor = escalations[len(escalations)-1].Sequence
		}
		return json.Marshal(map[string]any{"messages": messages, "self": self, "peers": peers, "escalations": escalations, "next_escalation_cursor": cursor, "has_more_escalations": more})
	case "agent_task_graph":
		var in struct {
			MessageID string `json:"message_id"`
			Limit     *int   `json:"limit"`
		}
		if err := decodeCoordination(input, &in); err != nil {
			return nil, err
		}
		if err := requireCoordMessageID(in.MessageID); err != nil {
			return nil, err
		}
		limit := 100
		if in.Limit != nil {
			limit = *in.Limit
		}
		if limit < 1 || limit > 200 {
			return nil, fmt.Errorf("limit must be 1..200")
		}
		graph, err := t.store.Graph(ctx, t.peerID, in.MessageID, limit)
		if err != nil {
			return nil, err
		}
		return json.Marshal(graph)
	case "agent_task_status":
		var in struct {
			MessageID string `json:"message_id"`
			After     int64  `json:"after_revision"`
			Limit     *int   `json:"limit"`
		}
		if err := decodeCoordination(input, &in); err != nil {
			return nil, err
		}
		if err := requireCoordMessageID(in.MessageID); err != nil {
			return nil, err
		}
		limit := 50
		if in.Limit != nil {
			limit = *in.Limit
		}
		if in.After < 0 || limit < 1 || limit > 200 {
			return nil, fmt.Errorf("nonnegative after_revision and limit 1..200 required")
		}
		message, err := t.store.Read(ctx, t.peerID, in.MessageID)
		if err != nil {
			return nil, err
		}
		events, err := t.store.MessageEvents(ctx, t.peerID, in.MessageID, in.After, limit+1)
		if err != nil {
			return nil, err
		}
		more := len(events) > limit
		if more {
			events = events[:limit]
		}
		next := in.After
		if len(events) > 0 {
			next = events[len(events)-1].Revision
			// A transition between the first read and replay must not expose a
			// message snapshot older than one of the returned events.
			if next > message.Revision {
				message, err = t.store.Read(ctx, t.peerID, in.MessageID)
				if err != nil {
					return nil, err
				}
			}
		}
		return json.Marshal(map[string]any{"message": message, "events": events, "next_revision": next, "has_more": more})
	case "agent_await":
		return t.await(ctx, input)
	case "agent_cancel":
		var in struct {
			MessageID string          `json:"message_id"`
			Revision  *int64          `json:"expected_revision"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := decodeCoordination(input, &in); err != nil {
			return nil, err
		}
		if err := requireCoordMessageID(in.MessageID); err != nil {
			return nil, err
		}
		if in.Revision != nil && *in.Revision < 1 {
			return nil, fmt.Errorf("expected_revision must be positive")
		}
		if len(in.Payload) == 0 {
			in.Payload = json.RawMessage(`{}`)
		}
		if err := validateCoordPayload(in.Payload); err != nil {
			return nil, err
		}
		current, err := t.store.Read(ctx, t.peerID, in.MessageID)
		if err != nil {
			return nil, err
		}
		revision := current.Revision
		if in.Revision != nil {
			revision = *in.Revision
		}
		message, err := t.store.Transition(ctx, t.peerID, in.MessageID, revision, "cancelled", in.Payload)
		if err != nil {
			return nil, err
		}
		return coordinationMessageOutput(message)
	default:
		return nil, fmt.Errorf("unknown coordination tool")
	}
}

func (t *coordinationTool) await(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		MessageID string   `json:"message_id"`
		After     *int64   `json:"after_revision"`
		Seconds   *float64 `json:"wait_seconds"`
	}
	if err := decodeCoordination(input, &in); err != nil {
		return nil, err
	}
	if err := requireCoordMessageID(in.MessageID); err != nil {
		return nil, err
	}
	seconds := 30.0
	if in.Seconds != nil {
		seconds = *in.Seconds
	}
	if seconds < 0 || seconds > 30 {
		return nil, fmt.Errorf("wait_seconds must be 0..30")
	}
	if in.After != nil && *in.After < 0 {
		return nil, fmt.Errorf("after_revision must be nonnegative")
	}
	current, err := t.store.Read(ctx, t.peerID, in.MessageID)
	if err != nil {
		return nil, err
	}
	after := current.Revision
	if in.After != nil {
		after = *in.After
	}
	output := func(message coordination.Message, timedOut bool) (json.RawMessage, error) {
		return json.Marshal(map[string]any{"message": message, "timed_out": timedOut})
	}
	if current.State.Terminal() || current.Revision > after {
		return output(current, false)
	}
	if seconds == 0 {
		return output(current, true)
	}
	waiting, cancel := context.WithTimeout(ctx, time.Duration(seconds*float64(time.Second)))
	defer cancel()
	message, err := t.store.Await(waiting, t.peerID, in.MessageID, after)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if waiting.Err() == context.DeadlineExceeded {
			current, readErr := t.store.Read(ctx, t.peerID, in.MessageID)
			if readErr != nil {
				return nil, readErr
			}
			return output(current, !current.State.Terminal() && current.Revision <= after)
		}
		return nil, err
	}
	return output(message, false)
}
