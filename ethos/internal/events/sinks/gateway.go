package sinks

import (
	"context"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/events"
)

// GatewaySink publishes a human-readable summary of a CompletionEvent back
// over the bridge channel the session originated from. The actual transport
// is decoupled via SendFunc — wire the bridge's existing SSE fanout here.
type GatewaySink struct {
	channel string
	chatKey string
	send    SendFunc
}

// SendFunc is the bridge's outbound send primitive. channel is the originating
// bridge channel (e.g. "bridge:whatsapp"), chatKey identifies the conversation.
type SendFunc func(channel, chatKey, text string) error

// NewGatewaySink returns a GatewaySink that pushes summaries back to the given
// channel/chatKey pair using send.
func NewGatewaySink(channel, chatKey string, send SendFunc) *GatewaySink {
	return &GatewaySink{channel: channel, chatKey: chatKey, send: send}
}

// Name implements events.Sink.
func (s *GatewaySink) Name() string { return "gateway" }

// Send formats evt as a short human-readable message and delivers it via the
// injected SendFunc.
func (s *GatewaySink) Send(_ context.Context, evt events.CompletionEvent) error {
	text := formatSummary(evt)
	if err := s.send(s.channel, s.chatKey, text); err != nil {
		return fmt.Errorf("gateway sink: %w", err)
	}
	return nil
}

func formatSummary(evt events.CompletionEvent) string {
	artefactCount := len(evt.Artefacts)
	errCount := len(evt.Errors)

	msg := fmt.Sprintf(
		"[%s] Session %s completed in %dms (cost $%.4f). Artefacts: %d.",
		evt.Outcome, evt.SessionID, evt.DurationMs, evt.CostUSD, artefactCount,
	)
	if errCount > 0 {
		msg += fmt.Sprintf(" Errors: %d.", errCount)
	}
	return msg
}
