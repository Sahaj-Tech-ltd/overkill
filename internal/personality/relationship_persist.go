// Package personality — relationship state persistence (disk save/load).
package personality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SaveToFile persists the relationship state atomically AND
// appends a durable event-log entry alongside the snapshot. Call
// on session end so the next boot can read the accumulated beats /
// milestones. The event log is defense-in-depth: if the snapshot is
// corrupted or wiped, LoadFromFile falls back to replaying the log.
func (r *RelationshipTracker) SaveToFile(path string) error {
	if path == "" || r == nil {
		return nil
	}
	state := r.State()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("personality: relationship mkdir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("personality: relationship marshal: %w", err)
	}
	// Append to the event log BEFORE rewriting the snapshot so a
	// crash between the two leaves us with a recoverable log entry.
	_ = NewEventLog(path).Append(data)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("personality: relationship write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("personality: relationship rename: %w", err)
	}
	return nil
}

// LoadFromFile reads a previously-persisted state into the tracker.
// Missing file is fine (cold start). Existing milestones are merged
// in — beats are NOT replayed through hooks (hooks fire on RecordBeat,
// not on Load) so a freshly-loaded tracker won't accidentally trigger
// "first PR merged!" again.
func (r *RelationshipTracker) LoadFromFile(path string) error {
	if path == "" || r == nil {
		return nil
	}
	// Validate-as-we-load: the snapshot is preferred, but if it
	// doesn't parse into RelationshipState we fall back to the
	// event log's latest good entry instead of cold-starting.
	valid := func(b []byte) bool {
		var tmp RelationshipState
		return json.Unmarshal(b, &tmp) == nil
	}
	data, err := LoadWithFallback(path, NewEventLog(path), valid)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// Snapshot AND log both unreadable. Treat as cold start
		// rather than fatal — the user's session shouldn't fail to
		// boot because of a permission glitch on a state file.
		return nil
	}
	if len(data) == 0 {
		return nil
	}
	var state RelationshipState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("personality: relationship parse: %w", err)
	}
	r.mu.Lock()
	r.state = state
	if r.state.Milestones == nil {
		r.state.Milestones = make(map[BeatType]bool)
	}
	r.mu.Unlock()
	return nil
}
