package agent

// LearningRecorder is the tiny interface the agent uses to feed
// recovered-from-error events into a self-learning pipeline (master plan
// §6.2). The real implementation lives in internal/skills/learn_trigger.go
// — defined here as an interface so the agent doesn't take a dep on skills
// just for this hook.
//
// Semantics:
//   - The agent calls RecordSuccess(class) only on the NEXT successful Run
//     that follows a Run which failed with that class. This is the
//     "recovered from X" signal — exactly what self-learning wants to
//     count, not arbitrary task completions.
//   - class is the diagnostic ladder error class ("compile", "test",
//     "runtime", "lint", "network", "unknown") — same key the escalator
//     uses (§4.13). Same class keys = aligned learning across the two
//     systems.
type LearningRecorder interface {
	RecordSuccess(class string) bool
}

// SetLearningRecorder installs the recovery-success recorder. Pass nil to
// disable.
func (a *Agent) SetLearningRecorder(r LearningRecorder) {
	a.mu.Lock()
	a.learningRecorder = r
	a.mu.Unlock()
}

// recordRecoverySuccess fires the learning hook (if installed) and clears
// the in-flight class so the next plain success doesn't double-count.
// Also fires the §6.3 first-success beat so the relationship arc
// captures the milestone. Best-effort: panic-recovered, errors
// swallowed — never blocks Run().
func (a *Agent) recordRecoverySuccess() {
	a.mu.Lock()
	cls := a.lastErrorClass
	r := a.learningRecorder
	a.lastErrorClass = ""
	a.mu.Unlock()

	// §6.3 beat — fire on every successful Run; the recorder's
	// milestone map dedupes first-of-kind so this is cheap.
	a.recordBeat(BeatFirstSuccess, cls)

	if cls == "" || r == nil {
		return
	}
	defer func() { _ = recover() }()
	r.RecordSuccess(cls)
}
