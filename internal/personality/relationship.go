// Package personality — relationship tracking: beats, milestones,
// session counting, and context-aware opener generation.
//
// Clock: RelationshipTracker injects its own clock (now field + SetClock)
// for testability. Other components in this package use time.Now() directly
// — there is no shared TimeProvider interface yet.
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
	now   func() time.Time

	// hooks fire on every RecordBeat. Failures are swallowed (best-effort).
	// First-of-kind hooks fire only on the first time a beat type is seen.
	hooks      []BeatHook
	firstHooks []BeatHook
}

// BeatHook is a callback invoked on RecordBeat. Returning an error is allowed
// but ignored (the relationship tracker is observability-only).
type BeatHook func(beat Beat)

// OnBeat registers a hook fired on every recorded beat.
func (r *RelationshipTracker) OnBeat(fn BeatHook) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.hooks = append(r.hooks, fn)
	r.mu.Unlock()
}

// OnFirstBeat registers a hook fired only the first time a given beat type
// is recorded — useful for "first commit ever" / "first rollback ever"
// celebrations or alerts.
func (r *RelationshipTracker) OnFirstBeat(fn BeatHook) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.firstHooks = append(r.firstHooks, fn)
	r.mu.Unlock()
}

func NewRelationshipTracker() *RelationshipTracker {
	return &RelationshipTracker{
		state: RelationshipState{
			Beats:      []Beat{},
			Milestones: make(map[BeatType]bool),
			Notes:      []string{},
		},
		now: time.Now,
	}
}

// SetClock overrides the clock used by the tracker. Intended for tests.
func (r *RelationshipTracker) SetClock(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now == nil {
		now = time.Now
	}
	r.now = now
}

func (r *RelationshipTracker) RecordBeat(beatType BeatType, context string, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
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

	firstTime := !r.state.Milestones[beatType]
	if firstTime {
		r.state.Milestones[beatType] = true
	}

	// Snapshot hooks under lock; fire after release so a slow hook never
	// blocks Record.
	hooks := append([]BeatHook(nil), r.hooks...)
	var firsts []BeatHook
	if firstTime {
		firsts = append([]BeatHook(nil), r.firstHooks...)
	}
	go func() {
		defer func() { _ = recover() }()
		for _, h := range hooks {
			h(beat)
		}
		for _, h := range firsts {
			h(beat)
		}
	}()
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

	nowFn := r.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn()
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
