package personality

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRelationship_PersistRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rel.json")

	r := NewRelationshipTracker()
	r.RecordBeat(BeatFirstPR, "shipped first feature", "sess1")
	r.RecordBeat(BeatFirstSkill, "wrote red-team.md", "sess1")

	if err := r.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	r2 := NewRelationshipTracker()
	if err := r2.LoadFromFile(path); err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	state := r2.State()
	if len(state.Beats) != 2 {
		t.Errorf("expected 2 beats after load, got %d", len(state.Beats))
	}
	if !state.Milestones[BeatFirstPR] || !state.Milestones[BeatFirstSkill] {
		t.Errorf("milestones not restored: %v", state.Milestones)
	}
}

func TestRelationship_LoadMissingFileIsOK(t *testing.T) {
	r := NewRelationshipTracker()
	if err := r.LoadFromFile("/nonexistent/x.json"); err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
}

func TestRelationship_HooksDoNotFireOnLoad(t *testing.T) {
	// Loading historical beats from disk MUST NOT re-fire the
	// first-of-kind hooks — that would re-celebrate "first PR
	// merged!" on every boot.
	dir := t.TempDir()
	path := filepath.Join(dir, "rel.json")

	seed := NewRelationshipTracker()
	seed.RecordBeat(BeatFirstPR, "x", "s1")
	_ = seed.SaveToFile(path)

	r := NewRelationshipTracker()
	fired := 0
	r.OnFirstBeat(func(Beat) { fired++ })
	if err := r.LoadFromFile(path); err != nil {
		t.Fatal(err)
	}
	if fired != 0 {
		t.Errorf("hooks should not fire on load, fired %d times", fired)
	}
}

func TestRelationship_DedupsRepeatBeats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rel.json")

	r := NewRelationshipTracker()
	// Hooks fire in a goroutine — sync via channel and mutex so the
	// counter read happens after the worker, and we keep the race
	// detector happy.
	var mu sync.Mutex
	first := 0
	fired := make(chan struct{}, 8)
	r.OnFirstBeat(func(Beat) {
		mu.Lock()
		first++
		mu.Unlock()
		fired <- struct{}{}
	})
	r.OnBeat(func(Beat) { fired <- struct{}{} })

	r.RecordBeat(BeatFirstFailure, "boom", "s1")
	r.RecordBeat(BeatFirstFailure, "another", "s1")
	r.RecordBeat(BeatFirstFailure, "and again", "s1")

	// 3 OnBeat + 1 OnFirstBeat = 4 deliveries; wait for all.
	for i := 0; i < 4; i++ {
		select {
		case <-fired:
		case <-time.After(time.Second):
			t.Fatalf("hook timeout at iter %d", i)
		}
	}
	mu.Lock()
	got := first
	mu.Unlock()
	if got != 1 {
		t.Errorf("first-of-kind hook should fire ONCE, got %d", got)
	}
	_ = r.SaveToFile(path)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
