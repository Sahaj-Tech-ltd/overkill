package tools

import (
	"context"
	"encoding/json"
)

type guardedTool struct {
	original Tool
	guard    ExecutionGuard
}

func (t *guardedTool) Name() string { return t.original.Name() }
func (t *guardedTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return t.guard(ctx, t.original, input)
}
func (t *guardedTool) Available() bool { return toolAvailable(t.original) }
func (t *guardedTool) IsConcurrencySafe(input json.RawMessage) bool {
	if tool, ok := t.original.(ConcurrencySafeTool); ok {
		return tool.IsConcurrencySafe(input)
	}
	return false
}
func (t *guardedTool) InterruptBehavior() InterruptBehavior {
	if tool, ok := t.original.(InterruptibleTool); ok {
		return tool.InterruptBehavior()
	}
	return InterruptBlock
}

type toolDescription interface{ Description() string }
type toolSchema interface{ Schema() json.RawMessage }
type guardedDescription struct {
	*guardedTool
	description toolDescription
}

func (t *guardedDescription) Description() string { return t.description.Description() }

type guardedSchema struct {
	*guardedTool
	schema toolSchema
}

func (t *guardedSchema) Schema() json.RawMessage { return t.schema.Schema() }

type guardedMetadata struct {
	*guardedTool
	description toolDescription
	schema      toolSchema
}

func (t *guardedMetadata) Description() string     { return t.description.Description() }
func (t *guardedMetadata) Schema() json.RawMessage { return t.schema.Schema() }

// Preserve the presence of optional metadata, not just its value: adding a nil
// Schema method to a native tool would erase the agent's built-in definition.
func guardedInvocation(original Tool, guard ExecutionGuard) Tool {
	if guard == nil {
		return original
	}
	base := &guardedTool{original: original, guard: guard}
	description, described := original.(toolDescription)
	schema, schemed := original.(toolSchema)
	switch {
	case described && schemed:
		return &guardedMetadata{base, description, schema}
	case described:
		return &guardedDescription{base, description}
	case schemed:
		return &guardedSchema{base, schema}
	default:
		return base
	}
}
