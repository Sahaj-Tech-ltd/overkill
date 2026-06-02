package agent

// BeatRecorder is the tiny interface the agent uses to fire relationship
// milestones (master plan §6.3 beat detection hooks). Implemented by
// personality.RelationshipTracker via a small adapter in cmd/overkill.
//
// The agent doesn't import internal/personality directly — keeps the
// dependency one-way (agent ← personality, never agent → personality).
type BeatRecorder interface {
	// RecordBeat fires a single beat of the given type with a short
	// context string. SessionID lets persistence track which session
	// each beat came from. Errors are intentionally absent — beat
	// recording is fire-and-forget; failures must not block the agent.
	RecordBeat(beatType string, contextText string, sessionID string)
}

// Known beat types — string-typed so the agent doesn't need to import
// personality's enum. The recorder adapter on the personality side
// translates these to typed BeatType values.
const (
	BeatFirstFailure  = "first_failure"
	BeatFirstSuccess  = "first_success"
	BeatFirstRollback = "first_rollback"
)

// SetBeatRecorder wires the recorder. Pass nil to disable.
func (a *Agent) SetBeatRecorder(r BeatRecorder) {
	a.mu.Lock()
	a.beatRecorder = r
	a.mu.Unlock()
}

// recordBeat is the agent-side helper used by emitRecovery + the
// success-path hook. Nil-safe and panic-recovered.
func (a *Agent) recordBeat(beatType, context string) {
	a.mu.RLock()
	r := a.beatRecorder
	sid := a.SessionID()
	a.mu.RUnlock()
	if r == nil {
		return
	}
	defer func() { _ = recover() }()
	r.RecordBeat(beatType, context, sid)
}
