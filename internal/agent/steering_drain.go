package agent

import "github.com/Sahaj-Tech-ltd/overkill/internal/providers"

// drainSteering pulls any queued steering messages off the SteeringQueue
// and appends them to history so the NEXT model call sees them. Called
// between tool iterations in the react and stream loops — this is the
// mid-turn injection point promised by master plan §9 PicoClaw steering
// + §4.1 "Interrupt and redirect mid-task". Different from the §pkg/tui
// prompt queue, which sits BEFORE a turn (drains on Done).
//
// Returns true when at least one message was drained — callers may emit
// an event for observability.
//
// Defensive: nil queue, empty drain, panic during append all no-op.
func (a *Agent) drainSteering() bool {
	a.mu.RLock()
	sq := a.steering
	a.mu.RUnlock()
	if sq == nil {
		return false
	}
	msgs := sq.Drained()
	if len(msgs) == 0 {
		return false
	}
	defer func() { _ = recover() }()
	for _, sm := range msgs {
		role := sm.Role
		if role == "" {
			role = "system"
		}
		a.appendMessage(providers.Message{
			Role:    role,
			Content: sm.Content,
		})
	}
	a.emit("steering_drained", map[string]any{
		"count":      len(msgs),
		"session_id": a.sessionID,
	})
	return true
}
