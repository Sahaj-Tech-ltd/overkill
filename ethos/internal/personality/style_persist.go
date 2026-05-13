// Package personality — disk persistence + "consecutive sessions"
// baseline drift logic for the two-layer style model (master plan
// §4.16).
//
// Short-term state (in-memory only) flips immediately on user input.
// Baseline state ONLY moves after N=5 consecutive sessions show the
// same dominant pattern — sustained signal, not noise. This is the
// "he's having a week" vs "he's always like this" distinction.
package personality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// stylePersistState is the on-disk shape. Tracks baseline, the last
// session's distilled style, and the consecutive-match counter so
// boot can resume mid-streak.
type stylePersistState struct {
	Baseline     *WorkingStyle `json:"baseline"`
	LastSession  *WorkingStyle `json:"last_session,omitempty"`
	StreakLength int           `json:"streak_length"`
}

// SaveToFile writes the inferencer's baseline + streak state to path.
// Caller on session-end after CommitSession so the next boot resumes
// with the same drift count.
func (si *StyleInferencer) SaveToFile(path string) error {
	if path == "" || si == nil {
		return nil
	}
	state := stylePersistState{
		Baseline:     si.baseline,
		LastSession:  si.shortTerm, // distilled view of this session
		StreakLength: si.sessionCount,
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("personality: style mkdir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("personality: style marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("personality: style write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("personality: style rename: %w", err)
	}
	return nil
}

// LoadFromFile seeds the inferencer from a saved state. Missing file
// is fine (cold start — baseline stays at the constructor defaults).
func (si *StyleInferencer) LoadFromFile(path string) error {
	if path == "" || si == nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("personality: style load: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var state stylePersistState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("personality: style parse: %w", err)
	}
	if state.Baseline != nil {
		si.SetBaseline(state.Baseline)
	}
	si.sessionCount = state.StreakLength
	// Note: shortTerm doesn't seed from LastSession — it starts fresh
	// each session and observes the user's actual messages. LastSession
	// is recorded for ConsecutiveSessionCommit's comparison instead.
	si.lastCommittedStyle = state.LastSession
	return nil
}

// ConsecutiveSessionCommit replaces CommitSession's count-only logic
// with "5 consecutive sessions of the same dominant pattern → promote
// to baseline" per master plan §4.16.
//
// Decision flow:
//  1. Compare THIS session's shortTerm with the previous session's
//     committed style (or the baseline on first run).
//  2. If matched → increment streak. When streak >= 5, promote
//     shortTerm to baseline and reset streak to 0.
//  3. If diverged → reset streak to 1 (this session counts as a
//     fresh start of a new pattern).
//
// "Match" = Communication AND Approach AND ResponseExpect all agree
// (FrustrationTrigger ignored because it's noisy per-session).
//
// Drop-in replacement for the existing CommitSession — callers should
// switch to this on session-end. The old method stays for backward
// compatibility.
func (si *StyleInferencer) ConsecutiveSessionCommit() (promoted bool) {
	if si == nil || si.shortTerm == nil {
		return false
	}
	prev := si.lastCommittedStyle
	if prev == nil {
		prev = si.baseline
	}
	matched := stylesMatch(si.shortTerm, prev)
	if matched {
		si.sessionCount++
	} else {
		// Fresh streak — this session is the first match of itself.
		si.sessionCount = 1
	}
	// Snapshot for next session's comparison.
	dup := *si.shortTerm
	dup.DomainTerms = append([]string{}, si.shortTerm.DomainTerms...)
	si.lastCommittedStyle = &dup

	if si.sessionCount >= si.sessionsForBaseline {
		si.baseline = &WorkingStyle{
			Communication:      si.shortTerm.Communication,
			ResponseExpect:     si.shortTerm.ResponseExpect,
			FrustrationTrigger: si.shortTerm.FrustrationTrigger,
			Approach:           si.shortTerm.Approach,
			DomainTerms:        append([]string{}, si.shortTerm.DomainTerms...),
		}
		si.sessionCount = 0
		return true
	}
	return false
}

// stylesMatch reports whether two styles agree on the dimensions that
// drive baseline drift. FrustrationTrigger is excluded — it's
// per-session and dropping out of the comparison stops a single
// rough week from preventing baseline promotion.
func stylesMatch(a, b *WorkingStyle) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Communication != b.Communication {
		return false
	}
	if a.ResponseExpect != b.ResponseExpect {
		return false
	}
	if a.Approach != b.Approach {
		return false
	}
	return true
}
