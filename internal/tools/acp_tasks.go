package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
)

// ACPRemote is trusted application configuration. Credentials never appear in
// model tool arguments, schemas, or receipts. A name fixes the destination.
type ACPRemote struct {
	URL   string
	Token string
}
type acpTaskTool struct {
	name  string
	peers map[string]ACPRemote
}

func NewACPTaskTools(peers map[string]ACPRemote) []Tool {
	if len(peers) == 0 {
		return nil
	}
	copy := make(map[string]ACPRemote, len(peers))
	for name, peer := range peers {
		copy[name] = peer
	}
	var result []Tool
	for _, name := range []string{"acp_submit", "acp_status", "acp_await", "acp_cancel", "acp_sessions"} {
		result = append(result, &acpTaskTool{name: name, peers: copy})
	}
	return result
}
func (t *acpTaskTool) Name() string { return t.name }
func (t *acpTaskTool) Description() string {
	switch t.name {
	case "acp_sessions":
		return "List persisted remote session IDs for a configured named external ACP peer; select an existing session before submitting work."
	case "acp_submit":
		return "Submit a durable task to a configured named external ACP peer. Returns its handle immediately; use the same idempotency_key/session/content after an uncertain response. Accepted/queued is not completed."
	case "acp_status":
		return "Read a durable external ACP task and its recorded reply/status. Interrupted means effects are uncertain; do not automatically resubmit."
	case "acp_await":
		return "Wait up to 25,000 ms for a terminal durable task state; otherwise return its latest state. Ending this observation does not cancel the task."
	default:
		return "Request cancellation of a durable external task. Cancellation does not roll back effects already performed."
	}
}
func (t *acpTaskTool) Schema() json.RawMessage {
	names := make([]string, 0, len(t.peers))
	for name := range t.peers {
		names = append(names, name)
	}
	sort.Strings(names)
	properties := map[string]any{"peer": map[string]any{"type": "string", "enum": names, "description": "Configured peer name; destination and credentials are application-owned."}}
	required := []string{"peer"}
	if t.name == "acp_submit" {
		properties["session"] = map[string]any{"type": "string", "description": "Existing remote session ID."}
		properties["content"] = map[string]any{"type": "string"}
		properties["idempotency_key"] = map[string]any{"type": "string", "description": "Stable request key reused across retries; never change body under the same key."}
		required = append(required, "session", "content", "idempotency_key")
	} else if t.name != "acp_sessions" {
		properties["task_id"] = map[string]any{"type": "string"}
		required = append(required, "task_id")
	}
	if t.name == "acp_await" {
		properties["wait_ms"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 25000}
		required = append(required, "wait_ms")
	}
	result, _ := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
	return result
}
func (t *acpTaskTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Peer    string `json:"peer"`
		Session string `json:"session"`
		Content string `json:"content"`
		Key     string `json:"idempotency_key"`
		TaskID  string `json:"task_id"`
		WaitMS  *int   `json:"wait_ms"`
	}
	if err := t.decode(input, &in); err != nil {
		return nil, fmt.Errorf("%s: %w", t.name, err)
	}
	peer, ok := t.peers[in.Peer]
	if !ok {
		return nil, fmt.Errorf("%s: configured peer required", t.name)
	}
	if strings.TrimSpace(peer.URL) == "" || peer.Token == "" {
		return nil, fmt.Errorf("%s: peer is unavailable", t.name)
	}
	client := acp.NewClient(peer.URL, peer.Token)
	client.HTTP = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if t.name == "acp_sessions" {
		sessions, err := client.ListSessions(ctx)
		if err != nil {
			return nil, fmt.Errorf("acp_sessions: %w", err)
		}
		return json.Marshal(map[string]any{"sessions": sessions})
	}
	var task acp.Task
	var err error
	switch t.name {
	case "acp_submit":
		if in.Session == "" || strings.TrimSpace(in.Content) == "" || strings.TrimSpace(in.Key) == "" || in.TaskID != "" || in.WaitMS != nil {
			return nil, fmt.Errorf("acp_submit: existing session,content,idempotency_key required; status fields forbidden")
		}
		task, err = client.SubmitTask(ctx, acp.TaskSubmitRequest{From: "overkill", Content: in.Content, SessionID: in.Session, IdempotencyKey: in.Key})
	case "acp_status", "acp_await", "acp_cancel":
		if in.TaskID == "" || in.Session != "" || in.Content != "" || in.Key != "" {
			return nil, fmt.Errorf("%s: task_id required; submission fields forbidden", t.name)
		}
		if t.name == "acp_await" {
			if in.WaitMS == nil || *in.WaitMS < 0 || *in.WaitMS > 25000 {
				return nil, fmt.Errorf("acp_await: wait_ms between 0 and 25000 required")
			}
			task, err = client.AwaitTask(ctx, in.TaskID, time.Duration(*in.WaitMS)*time.Millisecond)
		} else {
			if in.WaitMS != nil {
				return nil, fmt.Errorf("%s: wait_ms is only allowed for acp_await", t.name)
			}
			if t.name == "acp_status" {
				task, err = client.TaskStatus(ctx, in.TaskID)
			} else {
				task, err = client.CancelTask(ctx, in.TaskID)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", t.name, err)
	}
	return json.Marshal(task)
}

func (t *acpTaskTool) decode(input json.RawMessage, target any) error {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(t.Schema(), &schema); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for key := range schema.Properties {
		allowed[key] = true
	}
	return decodeACPObject(input, target, allowed)
}
func decodeACPObject(input json.RawMessage, target any, allowed map[string]bool) error {
	if len(input) > 72*1024 {
		return fmt.Errorf("bounded object required")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("object required")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return fmt.Errorf("duplicate field")
		}
		if !allowed[key] {
			return fmt.Errorf("unknown field")
		}
		seen[key] = true
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("null field")
		}
	}
	if _, err = decoder.Token(); err != nil {
		return err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return fmt.Errorf("exactly one object required")
	}
	return decodeCoordination(input, target)
}

func (t *acpTaskTool) IsConcurrencySafe(json.RawMessage) bool {
	return t.name == "acp_status" || t.name == "acp_await" || t.name == "acp_sessions"
}
func (t *acpTaskTool) InterruptBehavior() InterruptBehavior {
	if t.IsConcurrencySafe(nil) {
		return InterruptCancel
	}
	return InterruptBlock
}
