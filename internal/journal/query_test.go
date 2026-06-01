package journal

import (
	"testing"
	"time"
)

func TestJournalQuery_Search_FindsByTitle(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)
	jq := NewJournalQuery(s)

	o1 := NewObservation(ObsBugfix, "fix nil pointer crash", "fixed crash in handler", "s1")
	o1.Timestamp = time.Now().UTC().Add(-2 * time.Hour)
	if err := s.Store(o1); err != nil {
		t.Fatalf("Store o1: %v", err)
	}

	o2 := NewObservation(ObsFeature, "add JWT authentication", "implemented JWT", "s2")
	o2.Timestamp = time.Now().UTC().Add(-time.Hour)
	if err := s.Store(o2); err != nil {
		t.Fatalf("Store o2: %v", err)
	}

	results := jq.Search("jwt", "", 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "add JWT authentication" {
		t.Errorf("expected title 'add JWT authentication', got %s", results[0].Title)
	}
}

func TestJournalQuery_Search_EmptyQueryReturnsRecent(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)
	jq := NewJournalQuery(s)

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

	results := jq.Search("", "", 0)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestJournalQuery_Search_FilterByType(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)
	jq := NewJournalQuery(s)

	o1 := NewObservation(ObsBugfix, "fix auth crash", "fixed nil in auth", "s1")
	o1.Timestamp = time.Now().UTC().Add(-2 * time.Hour)
	if err := s.Store(o1); err != nil {
		t.Fatalf("Store o1: %v", err)
	}

	o2 := NewObservation(ObsFeature, "add auth middleware", "implemented auth", "s2")
	o2.Timestamp = time.Now().UTC().Add(-time.Hour)
	if err := s.Store(o2); err != nil {
		t.Fatalf("Store o2: %v", err)
	}

	results := jq.Search("auth", ObsBugfix, 10)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Type != ObsBugfix {
		t.Errorf("expected type %s, got %s", ObsBugfix, results[0].Type)
	}
}

func TestJournalQuery_Timeline_ReturnsSurrounding(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)
	jq := NewJournalQuery(s)

	o1 := NewObservation(ObsBugfix, "first fix", "first", "s1")
	o1.Timestamp = time.Now().UTC().Add(-3 * time.Hour)
	if err := s.Store(o1); err != nil {
		t.Fatalf("Store o1: %v", err)
	}

	o2 := NewObservation(ObsFeature, "feature add", "second", "s2")
	o2.Timestamp = time.Now().UTC().Add(-2 * time.Hour)
	if err := s.Store(o2); err != nil {
		t.Fatalf("Store o2: %v", err)
	}

	o3 := NewObservation(ObsDecision, "anchor decision", "third", "s3")
	o3.Timestamp = time.Now().UTC().Add(-time.Hour)
	if err := s.Store(o3); err != nil {
		t.Fatalf("Store o3: %v", err)
	}

	o4 := NewObservation(ObsRefactor, "cleanup code", "fourth", "s4")
	o4.Timestamp = time.Now().UTC()
	if err := s.Store(o4); err != nil {
		t.Fatalf("Store o4: %v", err)
	}

	results := jq.Timeline(o3.ID, 1)
	if len(results) != 3 {
		t.Fatalf("expected 3 results (1 before + anchor + 1 after), got %d", len(results))
	}

	if results[0].ID != o2.ID {
		t.Errorf("expected first result ID %s, got %s", o2.ID, results[0].ID)
	}
	if results[1].ID != o3.ID {
		t.Errorf("expected anchor ID %s, got %s", o3.ID, results[1].ID)
	}
	if results[2].ID != o4.ID {
		t.Errorf("expected last result ID %s, got %s", o4.ID, results[2].ID)
	}
}

func TestJournalQuery_Get_ReturnsFull(t *testing.T) {
	dir := t.TempDir()
	s := NewObservationStore(dir)
	jq := NewJournalQuery(s)

	obs := NewObservation(ObsFeature, "add auth", "implemented JWT", "sess-1")
	obs.Facts = []string{"used HMAC-SHA256"}
	obs.Concepts = []string{"JWT"}
	obs.FilesModified = []string{"auth.go"}

	if err := s.Store(obs); err != nil {
		t.Fatalf("Store: %v", err)
	}

	got, err := jq.Get(obs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != obs.ID {
		t.Errorf("expected ID %s, got %s", obs.ID, got.ID)
	}
	if got.Narrative != obs.Narrative {
		t.Errorf("expected narrative %s, got %s", obs.Narrative, got.Narrative)
	}
	if len(got.Facts) != 1 {
		t.Errorf("expected 1 fact, got %d", len(got.Facts))
	}
	if len(got.FilesModified) != 1 {
		t.Errorf("expected 1 file modified, got %d", len(got.FilesModified))
	}
}
