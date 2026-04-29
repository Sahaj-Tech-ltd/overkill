package hooks

import (
	"context"
	"encoding/json"
)

type HookPoint string

const (
	BeforeToolCall   HookPoint = "before_tool_call"
	AfterToolCall    HookPoint = "after_tool_call"
	OnSessionStart   HookPoint = "on_session_start"
	OnSessionEnd     HookPoint = "on_session_end"
	OnError          HookPoint = "on_error"
	BeforeCompaction HookPoint = "before_compaction"
	AfterCompaction  HookPoint = "after_compaction"
)

type HookFunc func(ctx context.Context, event Event) (context.Context, error)

type Event struct {
	Point      HookPoint        `json:"point"`
	ToolName   string           `json:"tool_name,omitempty"`
	ToolInput  json.RawMessage  `json:"tool_input,omitempty"`
	ToolOutput json.RawMessage  `json:"tool_output,omitempty"`
	Error      error            `json:"-"`
	SessionID  string           `json:"session_id,omitempty"`
	Metadata   map[string]any   `json:"metadata,omitempty"`
}

type Hook struct {
	Name     string
	Point    HookPoint
	Fn       HookFunc
	Priority int
}
