package personality

import (
	"fmt"
	"sync"
	"time"
)

type BeatType string

const (
	BeatFirstFailure  BeatType = "first_failure"
	BeatFirstSuccess  BeatType = "first_success"
	BeatFirstPR       BeatType = "first_pr_merged"
	BeatFirstSkill    BeatType = "first_skill_created"
	BeatFirstRollback BeatType = "first_rollback"
	BeatFirstHighFive BeatType = "first_high_five"
	BeatFrustration   BeatType = "frustration_signal"
	BeatLateNight     BeatType = "late_night"
)

type Beat struct {
	Type      BeatType  `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Context   string    `json:"context"`
	SessionID string    `json:"session_id"`
}

type RelationshipState struct {
	TotalSessions     int               `json:"total_sessions"`
	TotalInteractions int               `json:"total_interactions"`
	FirstSeen         time.Time         `json:"first_seen"`
	LastSeen          time.Time         `json:"last_seen"`
	Beats             []Beat            `json:"beats"`
	Milestones        map[BeatType]bool `json:"milestones"`
	Notes             []string          `json:"notes"`
}

type RelationshipTracker struct {
	mu    sync.RWMutex
	state RelationshipState
}

func NewRelationshipTracker() *RelationshipTracker {
	return &RelationshipTracker{
		state: RelationshipState{
			Beats:      []Beat{},
			Milestones: make(map[BeatType]bool),
			Notes:      []string{},
		},
	}
}

func (r *RelationshipTracker) RecordBeat(beatType BeatType, context string, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	if r.state.FirstSeen.IsZero() {
		r.state.FirstSeen = now
	}
	r.state.LastSeen = now

	beat := Beat{
		Type:      beatType,
		Timestamp: now,
		Context:   context,
		SessionID: sessionID,
	}
	r.state.Beats = append(r.state.Beats, beat)

	if !r.state.Milestones[beatType] {
		r.state.Milestones[beatType] = true
	}
}

func (r *RelationshipTracker) RecordInteraction() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.state.TotalInteractions++
}

func (r *RelationshipTracker) State() RelationshipState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	beatsCopy := make([]Beat, len(r.state.Beats))
	copy(beatsCopy, r.state.Beats)

	milestonesCopy := make(map[BeatType]bool)
	for k, v := range r.state.Milestones {
		milestonesCopy[k] = v
	}

	notesCopy := make([]string, len(r.state.Notes))
	copy(notesCopy, r.state.Notes)

	return RelationshipState{
		TotalSessions:     r.state.TotalSessions,
		TotalInteractions: r.state.TotalInteractions,
		FirstSeen:         r.state.FirstSeen,
		LastSeen:          r.state.LastSeen,
		Beats:             beatsCopy,
		Milestones:        milestonesCopy,
		Notes:             notesCopy,
	}
}

func (r *RelationshipTracker) HasMilestone(beatType BeatType) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state.Milestones[beatType]
}

func (r *RelationshipTracker) SessionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state.TotalSessions
}

func (r *RelationshipTracker) IncrementSession() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.TotalSessions++
}

func (r *RelationshipTracker) Opener(agentName string, userName string, currentContext string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	userGreeting := ""
	if userName != "" {
		userGreeting = " " + userName
	}

	if r.state.TotalSessions == 0 && len(r.state.Beats) == 0 {
		return fmt.Sprintf("Hey%s, I'm %s. Let's build something.", userGreeting, agentName)
	}

	for _, beat := range r.state.Beats {
		if beat.Type == BeatFrustration {
			return fmt.Sprintf("Hey%s. Let's take it easy today.", userGreeting)
		}
	}

	now := time.Now()
	if now.Hour() >= 0 && now.Hour() < 5 {
		return "Still up? Respect the grind."
	}

	if r.state.LastSeen.After(now.Add(-24*time.Hour)) && currentContext != "" {
		return fmt.Sprintf("Back at %s huh? Want me to actually plan this time?", currentContext)
	}

	milestoneDescriptions := map[BeatType]string{
		BeatFirstPR:       "shipped a PR",
		BeatFirstSuccess:  "nailed a task",
		BeatFirstSkill:    "created a skill",
		BeatFirstRollback: "survived a rollback",
		BeatFirstHighFive: "celebrated a win",
	}

	for _, milestone := range []BeatType{BeatFirstPR, BeatFirstSuccess, BeatFirstSkill, BeatFirstRollback, BeatFirstHighFive} {
		if r.state.Milestones[milestone] {
			return fmt.Sprintf("Last time we %s. Ready for the next one%s?", milestoneDescriptions[milestone], userGreeting)
		}
	}

	return fmt.Sprintf("Hey%s. What are we working on?", userGreeting)
}
