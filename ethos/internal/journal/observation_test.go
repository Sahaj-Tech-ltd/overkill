package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewObservation_SetsDefaults(t *testing.T) {
	o := NewObservation(ObsBugfix, "fix crash", "fixed nil pointer", "sess-1")

	if o.ID == "" {
		t.Error("expected non-empty ID")
	}
	if o.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	if o.ContentHash == "" {
		t.Error("expected non-empty content hash")
	}
	if o.Type != ObsBugfix {
		t.Errorf("expected type %s, got %s", ObsBugfix, o.Type)
	}
	if o.Title != "fix crash" {
		t.Errorf("expected title 'fix crash', got %s", o.Title)
	}
	if o.SessionID != "sess-1" {
		t.Errorf("expected sessionID 'sess-1', got %s", o.SessionID)
	}
}

func TestObservation_ComputeHash_Deterministic(t *testing.T) {
	o1 := &Observation{SessionID: "s1", Title: "t1", Narrative: "n1"}
	o2 := &Observation{SessionID: "s1", Title: "t1", Narrative: "n1"}

	h1 := o1.ComputeHash()
	h2 := o2.ComputeHash()

	if h1 != h2 {
		t.Errorf("expected same hash for same inputs, got %s and %s", h1, h2)
	}
}

func TestObservation_ComputeHash_DifferentInputs(t *testing.T) {
	o1 := &Observation{SessionID: "s1", Title: "t1", Narrative: "n1"}
	o2 := &Observation{SessionID: "s1", Title: "t1", Narrative: "n2"}

	h1 := o1.ComputeHash()
	h2 := o2.ComputeHash()

	if h1 == h2 {
		t.Error("expected different hashes for different inputs")
	}
}

func TestObservation_Index_ReturnsCompact(t *testing.T) {
	now := time.Now().UTC()
	o := &Observation{
		ID:            "obs-1",
		Type:          ObsFeature,
		Title:         "add auth",
		Narrative:     "long narrative",
		Facts:         []string{"fact1"},
		Concepts:      []string{"concept1"},
		FilesRead:     []string{"a.go"},
		FilesModified: []string{"b.go"},
		SessionID:     "sess-1",
		Timestamp:     now,
		ContentHash:   "abc123",
	}

	idx := o.Index()

	if idx.ID != "obs-1" {
		t.Errorf("expected ID 'obs-1', got %s", idx.ID)
	}
	if idx.Type != ObsFeature {
		t.Errorf("expected type %s, got %s", ObsFeature, idx.Type)
	}
	if idx.Title != "add auth" {
		t.Errorf("expected title 'add auth', got %s", idx.Title)
	}
	if !idx.Timestamp.Equal(now) {
		t.Errorf("expected timestamp %v, got %v", now, idx.Timestamp)
	}
}

func TestObservationStore_Store_WritesFile(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)

	obs := NewObservation(ObsBugfix, "fix crash", "fixed nil pointer", "sess-1")
	if err := s.Store(obs); err != nil {
		t.Fatalf("Store: %v", err)
	}

	obsDir := filepath.Join(dir, "observations")
	files, err := os.ReadDir(obsDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	found := false
	for _, f := range files {
		if strings.HasPrefix(f.Name(), "observations-") && strings.HasSuffix(f.Name(), ".jsonl") {
			found = true
			data, err := os.ReadFile(filepath.Join(obsDir, f.Name()))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 1 {
				t.Fatalf("expected 1 line, got %d", len(lines))
			}
			if !strings.Contains(string(data), obs.ID) {
				t.Error("expected file to contain observation ID")
			}
		}
	}

	if !found {
		t.Error("expected observations JSONL file to be created")
	}
}

func TestObservationStore_Store_Dedup(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)

	obs := NewObservation(ObsBugfix, "fix crash", "fixed nil pointer", "sess-1")

	if err := s.Store(obs); err != nil {
		t.Fatalf("Store first: %v", err)
	}

	dup := &Observation{
		ID:            "different-id",
		Type:          obs.Type,
		Title:         obs.Title,
		Narrative:     obs.Narrative,
		SessionID:     obs.SessionID,
		Timestamp:     obs.Timestamp,
		ContentHash:   obs.ContentHash,
	}

	if err := s.Store(dup); err != nil {
		t.Fatalf("Store dup: %v", err)
	}

	got, err := s.Get(obs.ID)
	if err != nil {
		t.Fatalf("Get original: %v", err)
	}
	if got == nil {
		t.Fatal("expected to find original observation")
	}

	_, err = s.Get("different-id")
	if err == nil {
		t.Error("expected error for duplicate that was skipped")
	}
}

func TestObservationStore_Get_ReturnsFull(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)

	obs := NewObservation(ObsFeature, "add auth", "implemented JWT", "sess-2")
	obs.Facts = []string{"used HMAC-SHA256"}
	obs.Concepts = []string{"JWT", "auth"}
	obs.FilesRead = []string{"auth.go"}
	obs.FilesModified = []string{"auth.go", "middleware.go"}

	if err := s.Store(obs); err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, err := s.Get(obs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != obs.ID {
		t.Errorf("expected ID %s, got %s", obs.ID, got.ID)
	}
	if got.Type != obs.Type {
		t.Errorf("expected type %s, got %s", obs.Type, got.Type)
	}
	if got.Title != obs.Title {
		t.Errorf("expected title %s, got %s", obs.Title, got.Title)
	}
	if got.Narrative != obs.Narrative {
		t.Errorf("expected narrative %s, got %s", obs.Narrative, got.Narrative)
	}
	if len(got.Facts) != 1 || got.Facts[0] != "used HMAC-SHA256" {
		t.Errorf("expected facts ['used HMAC-SHA256'], got %v", got.Facts)
	}
	if len(got.Concepts) != 2 {
		t.Errorf("expected 2 concepts, got %d", len(got.Concepts))
	}
	if len(got.FilesRead) != 1 {
		t.Errorf("expected 1 file read, got %d", len(got.FilesRead))
	}
	if len(got.FilesModified) != 2 {
		t.Errorf("expected 2 files modified, got %d", len(got.FilesModified))
	}
	if got.SessionID != obs.SessionID {
		t.Errorf("expected sessionID %s, got %s", obs.SessionID, got.SessionID)
	}
}

func TestObservationStore_Get_NotFound(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)

	_, err := s.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent ID")
	}
}

func TestObservationStore_List_FilterByType(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)

	o1 := NewObservation(ObsBugfix, "fix crash", "fixed nil ptr", "s1")
	o1.Timestamp = time.Now().UTC().Add(-2 * time.Hour)
	if err := s.Store(o1); err != nil {
		t.Fatalf("Store o1: %v", err)
	}

	o2 := NewObservation(ObsFeature, "add auth", "implemented JWT", "s2")
	o2.Timestamp = time.Now().UTC().Add(-time.Hour)
	if err := s.Store(o2); err != nil {
		t.Fatalf("Store o2: %v", err)
	}

	o3 := NewObservation(ObsBugfix, "fix leak", "closed conn", "s3")
	o3.Timestamp = time.Now().UTC()
	if err := s.Store(o3); err != nil {
		t.Fatalf("Store o3: %v", err)
	}

	results := s.List(ObsBugfix, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 bugfix observations, got %d", len(results))
	}

	for _, idx := range results {
		if idx.Type != ObsBugfix {
			t.Errorf("expected type %s, got %s", ObsBugfix, idx.Type)
		}
	}
}

func TestObservationStore_List_EmptyTypeReturnsAll(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)

	o1 := NewObservation(ObsBugfix, "fix crash", "fixed nil ptr", "s1")
	o1.Timestamp = time.Now().UTC().Add(-2 * time.Hour)
	if err := s.Store(o1); err != nil {
		t.Fatalf("Store o1: %v", err)
	}

	o2 := NewObservation(ObsFeature, "add auth", "implemented JWT", "s2")
	o2.Timestamp = time.Now().UTC().Add(-time.Hour)
	if err := s.Store(o2); err != nil {
		t.Fatalf("Store o2: %v", err)
	}

	o3 := NewObservation(ObsDecision, "use postgres", "switched from sqlite", "s3")
	o3.Timestamp = time.Now().UTC()
	if err := s.Store(o3); err != nil {
		t.Fatalf("Store o3: %v", err)
	}

	results := s.List("", 0)
	if len(results) != 3 {
		t.Fatalf("expected 3 observations, got %d", len(results))
	}
}
