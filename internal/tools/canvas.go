package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// CanvasHandler is bound by the application to one persisted session. It never
// receives model-selected identity, a database, renderer code or filesystem path.
type CanvasHandler func(context.Context, string, string) (json.RawMessage, error)
type CanvasTool struct{ handler CanvasHandler }

func NewCanvasTool(handler CanvasHandler) *CanvasTool { return &CanvasTool{handler: handler} }
func (t *CanvasTool) Name() string                    { return "canvas" }
func (t *CanvasTool) Available() bool                 { return t.handler != nil }
func (t *CanvasTool) Description() string {
	return `Read or draw this chat's persistent native canvas. action=state reads; action=reset clears it; action=apply accepts exactly one JSONL mutation string. Use createSurface {surfaceId,catalog:"overkill.canvas.v1"}, surfaceUpdate {surfaceId,components:[{id,type,text? or textPath?,children?}]}, dataModelUpdate {surfaceId,contents:{...}}, then beginRendering {surfaceId,root}. Supported types: Text/Heading (exactly text or nonempty JSON Pointer textPath), Column/Row (children IDs), Divider. Updates replace all components or data; use state before changing an existing surface. deleteSurface {surfaceId} deletes. All identity is bound to this chat. No HTML, scripts, URLs, images or CSS. Limits:16 surfaces,128 components,depth16,text16KiB,data64KiB. Example apply jsonl: {"createSurface":{"surfaceId":"status","catalog":"overkill.canvas.v1"}}. Accepted updates are committed; rendering/export are separate UI operations.`
}
func (t *CanvasTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["state","apply","reset"]},"jsonl":{"type":"string","maxLength":262144,"description":"Exactly one custom canvas mutation JSON object, encoded as a string; only for action apply."}},"required":["action"],"additionalProperties":false}`)
}
func canvasInput(raw json.RawMessage) (string, string, error) {
	if len(raw) > 1024*1024 {
		return "", "", fmt.Errorf("canvas input exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", "", fmt.Errorf("canvas object required")
	}
	fields := map[string]string{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return "", "", err
		}
		key, ok := token.(string)
		if !ok || (key != "action" && key != "jsonl") {
			return "", "", fmt.Errorf("unknown canvas field")
		}
		if _, seen := fields[key]; seen {
			return "", "", fmt.Errorf("duplicate canvas field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return "", "", err
		}
		if len(value) == 0 || value[0] != '"' {
			return "", "", fmt.Errorf("canvas fields must be strings")
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return "", "", err
		}
		fields[key] = text
	}
	if _, err := decoder.Token(); err != nil {
		return "", "", err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", "", fmt.Errorf("one canvas input object required")
	}
	action, line := fields["action"], fields["jsonl"]
	if action != "state" && action != "apply" && action != "reset" {
		return "", "", fmt.Errorf("canvas action must be state, apply or reset")
	}
	if action == "apply" {
		if line == "" || len(line) > 256*1024 {
			return "", "", fmt.Errorf("apply requires one bounded jsonl mutation")
		}
	} else if _, present := fields["jsonl"]; present {
		return "", "", fmt.Errorf("jsonl is only accepted for apply")
	}
	return action, line, nil
}
func (t *CanvasTool) IsConcurrencySafe(input json.RawMessage) bool {
	action, _, err := canvasInput(input)
	return err == nil && action == "state"
}
func (t *CanvasTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	action, line, err := canvasInput(input)
	if err != nil {
		return nil, err
	}
	if !t.Available() {
		return nil, fmt.Errorf("canvas is unavailable for this chat")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return t.handler(ctx, action, line)
}
