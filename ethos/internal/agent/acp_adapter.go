package agent

import (
	"context"
)

// ACPEventType mirrors acp.AgentEventType but lives in the agent package to
// avoid an import cycle. The acp package adapter consumes these values via
// its acp.AgentEvent shape.
type ACPEventType int

const (
	ACPEventToken ACPEventType = iota
	ACPEventToolStart
	ACPEventToolOutput
	ACPEventDone
	ACPEventError
)

// ACPEvent is the agent-side counterpart to acp.AgentEvent.
type ACPEvent struct {
	Type     ACPEventType
	Content  string
	ToolName string
	ToolArgs string
	Error    error
}

// StreamACPRaw runs Stream and emits the protocol-friendly ACPEvent shape.
// The acp package wraps this via a tiny adapter (see cmd/overkill/tui.go).
func (a *Agent) StreamACPRaw(ctx context.Context, userInput string) (<-chan ACPEvent, error) {
	src, err := a.Stream(ctx, userInput)
	if err != nil {
		return nil, err
	}
	out := make(chan ACPEvent, 64)
	go func() {
		defer close(out)
		for ev := range src {
			ae := ACPEvent{Content: ev.Content}
			switch ev.Type {
			case EventToken:
				ae.Type = ACPEventToken
			case EventToolStart:
				ae.Type = ACPEventToolStart
				if ev.ToolCall != nil {
					ae.ToolName = ev.ToolCall.Name
					ae.ToolArgs = ev.ToolCall.Arguments
				}
			case EventToolOutput:
				ae.Type = ACPEventToolOutput
				if ev.ToolCall != nil {
					ae.ToolName = ev.ToolCall.Name
					ae.ToolArgs = ev.ToolCall.Arguments
				}
			case EventDone:
				ae.Type = ACPEventDone
			case EventError:
				ae.Type = ACPEventError
				ae.Error = ev.Error
			}
			out <- ae
		}
	}()
	return out, nil
}
