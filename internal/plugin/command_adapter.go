package plugin

import (
	"context"
	"strings"
)

// CommandID is the wire format for plugin slash-commands in the TUI: it
// looks like `plugin:<plugin>:<id>` so the central dispatch can route by
// prefix without ambiguity.
func CommandID(plugin, id string) string {
	return "plugin:" + plugin + ":" + id
}

// ParseCommandID splits a plugin command id into (plugin, id, ok).
func ParseCommandID(s string) (plugin, id string, ok bool) {
	if !strings.HasPrefix(s, "plugin:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(s, "plugin:")
	idx := strings.Index(rest, ":")
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

// Dispatch is the helper the TUI's dispatchCommandWithArgs uses. Given a
// prefixed command id and the user-supplied args string, it routes to the
// right plugin via the manager.
func (m *Manager) Dispatch(ctx context.Context, fullID, args string) error {
	plugin, id, ok := ParseCommandID(fullID)
	if !ok {
		return nil
	}
	return m.InvokeCommand(ctx, plugin, id, args)
}
