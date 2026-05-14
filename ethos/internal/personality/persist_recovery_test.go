package personality

import (
	"os"
	"path/filepath"
	"testing"
)

// Integration test: full round-trip across all three persisters
// shows the event log recovers state when the snapshot file has
// been wiped (the very scenario this work guards against — a
// hallucinating agent or a bad write nuking the file).

func TestRelationship_RecoversFromWipedSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "relationship-arc.json")

	// Save a populated state.
	src := NewRelationshipTracker()
	src.RecordBeat(BeatFirstSuccess, "compile", "sess-1")
	if err := src.SaveToFile(path); err != nil {
		t.Fatal(err)
	}

	// Simulate the adversarial / corruption case: snapshot is wiped.
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	// Load into a fresh tracker — must recover the milestone via
	// the event-log fallback.
	dst := NewRelationshipTracker()
	if err := dst.LoadFromFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if !dst.State().Milestones[BeatFirstSuccess] {
		t.Error("FirstSuccess milestone should survive snapshot wipe via event log")
	}
}

func TestFingerprint_RecoversFromCorruptedSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fingerprint.json")

	src := NewFingerprintTracker()
	if _, err := src.BootCheck(path, "claude-opus-4-7"); err != nil {
		t.Fatal(err)
	}
	if err := src.SaveToFile(path); err != nil {
		t.Fatal(err)
	}

	// Corrupt the snapshot.
	if err := os.WriteFile(path, []byte("not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := NewFingerprintTracker()
	if err := dst.LoadFromFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if dst.current == nil {
		t.Fatal("expected fingerprint recovered from log; got nil")
	}
	if dst.current.Family == "" && dst.current.Version == "" {
		t.Errorf("recovered fingerprint should have family/version populated: %+v", dst.current)
	}
}

func TestStyle_RecoversFromMissingSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "style.json")

	src := NewStyleInferencer()
	src.SetBaseline(&WorkingStyle{
		Communication:  "concise",
		ResponseExpect: "fast",
		Approach:       "pragmatic",
	})
	if err := src.SaveToFile(path); err != nil {
		t.Fatal(err)
	}

	// Wipe the snapshot.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	dst := NewStyleInferencer()
	if err := dst.LoadFromFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if dst.baseline == nil || dst.baseline.Communication != "concise" {
		t.Errorf("expected baseline recovered from log; got %+v", dst.baseline)
	}
}
