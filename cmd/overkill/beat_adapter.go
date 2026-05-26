package main

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
)

// beatRecorderAdapter bridges the personality RelationshipTracker into
// the agent's string-typed BeatRecorder interface. Keeps the agent
// package free of internal/personality and lets cmd/overkill own the
// translation between string beat-types and the typed BeatType enum.
type beatRecorderAdapter struct {
	tracker *personality.RelationshipTracker
}

var _ agent.BeatRecorder = (*beatRecorderAdapter)(nil)

func (b *beatRecorderAdapter) RecordBeat(beatType, context, sessionID string) {
	if b == nil || b.tracker == nil {
		return
	}
	// Translate the agent's stringly-typed constants into the
	// personality package's typed values. Unknown types fall through
	// — RecordBeat tolerates any BeatType (it just keys a map).
	t := personality.BeatType(beatType)
	b.tracker.RecordBeat(t, context, sessionID)
}
