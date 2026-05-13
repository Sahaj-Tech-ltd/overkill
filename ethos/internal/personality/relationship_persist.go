package personality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SaveToFile persists the relationship state atomically. Call on
// session end so the next boot can read the accumulated beats /
// milestones. The file is JSON because the state is small and
// human-debuggable beats binary KV.
func (r *RelationshipTracker) SaveToFile(path string) error {
	if path == "" || r == nil {
		return nil
	}
	state := r.State()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("personality: relationship mkdir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("personality: relationship marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
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
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("personality: relationship load: %w", err)
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
