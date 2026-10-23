package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolAdapter implements internal/tools.Tool by routing calls into a Manager.
// The wrapper name is `mcp:<server>:<tool>` so multiple servers exposing
// identically-named tools don't collide in the registry.
type ToolAdapter struct {
	manager  *Manager
	server   string
	toolName string
	full     string
}

// NewToolAdapter wraps one MCP tool for registration in the agent's tool registry.
func NewToolAdapter(m *Manager, server, tool string) *ToolAdapter {
	return &ToolAdapter{
		manager:  m,
		server:   server,
		toolName: tool,
		full:     "mcp:" + server + ":" + tool,
	}
}

func (a *ToolAdapter) Name() string { return a.full }

func (a *ToolAdapter) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	res, err := a.manager.Call(ctx, a.server, a.toolName, input)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.full, err)
	}
	return json.Marshal(map[string]any{
		"text":    res.Text,
		"isError": res.IsError,
		"content": res.Content,
	})
}

// Available hides stale or denied catalog entries without replacing native tools.
func (a *ToolAdapter) Available() bool { _, ok := a.definition(); return ok }
func (a *ToolAdapter) definition() (Tool, bool) {
	return a.manager.lookupTool(a.server, a.toolName)
}

func (a *ToolAdapter) Description() string {
	if definition, ok := a.definition(); ok {
		return definition.Description
	}
	return "MCP tool is unavailable"
}
func (a *ToolAdapter) Schema() json.RawMessage {
	if definition, ok := a.definition(); ok && len(definition.InputSchema) > 0 {
		return append(json.RawMessage(nil), definition.InputSchema...)
	}
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
