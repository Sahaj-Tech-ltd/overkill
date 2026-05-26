package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
)

// TagAddTool registers a (path, tag) annotation in the user's tag store.
type TagAddTool struct{ mgr *tags.Manager }

func NewTagAddTool(m *tags.Manager) *TagAddTool { return &TagAddTool{mgr: m} }
func (t *TagAddTool) Name() string              { return "tag_add" }
func (t *TagAddTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Path string `json:"path"`
		Tag  string `json:"tag"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if t.mgr == nil {
		return nil, fmt.Errorf("tag_add: tag manager not configured")
	}
	if err := t.mgr.Tag(in.Path, in.Tag, in.Note); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "path": in.Path, "tag": in.Tag})
}

// TagRemoveTool removes (path, tag) — or all tags on the path if Tag is empty.
type TagRemoveTool struct{ mgr *tags.Manager }

func NewTagRemoveTool(m *tags.Manager) *TagRemoveTool { return &TagRemoveTool{mgr: m} }
func (t *TagRemoveTool) Name() string                 { return "tag_remove" }
func (t *TagRemoveTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Path string `json:"path"`
		Tag  string `json:"tag"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if t.mgr == nil {
		return nil, fmt.Errorf("tag_remove: tag manager not configured")
	}
	if err := t.mgr.Untag(in.Path, in.Tag); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true})
}

// TagListTool lists tags, optionally filtered by tag name or path.
type TagListTool struct{ mgr *tags.Manager }

func NewTagListTool(m *tags.Manager) *TagListTool { return &TagListTool{mgr: m} }
func (t *TagListTool) Name() string               { return "tag_list" }
func (t *TagListTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Tag  string `json:"tag"`
		Path string `json:"path"`
	}
	if len(input) > 0 {
		_ = json.Unmarshal(input, &in)
	}
	if t.mgr == nil {
		return nil, fmt.Errorf("tag_list: tag manager not configured")
	}
	var entries []tags.Tag
	switch {
	case in.Tag != "":
		entries = t.mgr.ByTag(in.Tag)
	case in.Path != "":
		entries = t.mgr.ByPath(in.Path)
	default:
		entries = t.mgr.List()
	}
	return json.Marshal(map[string]any{"tags": entries, "count": len(entries)})
}
