package plugin

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolAdapter exposes a plugin-registered tool through the agent's
// internal/tools.Tool interface. Tool name format: plugin:<plugin>:<tool>.
type ToolAdapter struct {
	manager *Manager
	plugin  string
	tool    string
	full    string
}

// NewToolAdapter wraps a single plugin tool for registration in the agent.
func NewToolAdapter(m *Manager, plugin, tool string) *ToolAdapter {
	return &ToolAdapter{
		manager: m,
		plugin:  plugin,
		tool:    tool,
		full:    "plugin:" + plugin + ":" + tool,
	}
}

func (a *ToolAdapter) Name() string { return a.full }

func (a *ToolAdapter) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	raw, err := a.manager.CallTool(ctx, a.plugin, a.tool, input)
	if err != nil {
		b, mErr := json.Marshal(map[string]any{"error": err.Error()})
		if mErr != nil {
			// json.Marshal only fails on unserializable types;
			// a string error message is always serializable.
			// Return the marshal error so it isn't silently swallowed.
			return nil, fmt.Errorf("tool_adapter: marshal error response: %w (original: %w)", mErr, err)
		}
		return b, nil
	}
	// Pass through whatever the plugin returned. Most plugins respond with a
	// JSON object; if the response isn't valid JSON, wrap it as a string.
	if json.Valid(raw) {
		return raw, nil
	}
	return json.Marshal(map[string]any{"text": string(raw)})
}
