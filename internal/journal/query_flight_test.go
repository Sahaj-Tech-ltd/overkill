package journal

import (
	"testing"
	"time"
)

func TestSearchFlight_EmptyJournalNoError(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "s1")
	hits, err := r.SearchFlight(FlightSearchOptions{Query: "anything"})
	if err != nil {
		t.Errorf("empty journal should not error, got %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected no hits, got %d", len(hits))
	}
}

func TestSearchFlight_BasicMatch(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "s1")
	if err := r.RecordInput("how do I refactor the auth module?"); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordReply("Look at internal/auth/handlers.go first."); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordInput("what about payments?"); err != nil {
		t.Fatal(err)
	}

	// Substring search.
	hits, err := r.SearchFlight(FlightSearchOptions{Query: "auth"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Errorf("expected 2 auth hits, got %d", len(hits))
	}
	// Title must be a short preview.
	for _, h := range hits {
		if len(h.Title) == 0 {
			t.Errorf("empty title in %+v", h)
		}
	}
}

func TestSearchFlight_TypeFilter(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "s1")
	_ = r.RecordInput("question 1")
	_ = r.RecordReply("answer 1")
	_ = r.RecordInput("question 2")

	hits, err := r.SearchFlight(FlightSearchOptions{Type: EntryUserInput})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Errorf("expected 2 user_input hits, got %d", len(hits))
	}
}

func TestGetFlight_MissingIDReturnsNil(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "s1")
	got, err := r.GetFlight("does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing ID, got %+v", got)
	}
}

func TestGetFlight_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "s1")
	_ = r.RecordReply("the answer to life, the universe, and everything")

	hits, err := r.SearchFlight(FlightSearchOptions{Query: "universe"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	id := hits[0].ID

	entry, err := r.GetFlight(id)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("Get returned nil for ID just searched")
	}
	if entry.Content != "the answer to life, the universe, and everything" {
		t.Errorf("content mismatch: %q", entry.Content)
	}
}

func TestTimelineFlight_AnchorContext(t *testing.T) {
	dir := t.TempDir()
	r := NewFlightRecorder(dir, "s1")
	_ = r.RecordInput("step 1")
	time.Sleep(2 * time.Millisecond) // ensure distinct timestamps
	_ = r.RecordReply("doing step 1")
	time.Sleep(2 * time.Millisecond)
	_ = r.RecordInput("step 2 (anchor)")
	time.Sleep(2 * time.Millisecond)
	_ = r.RecordReply("doing step 2")
	time.Sleep(2 * time.Millisecond)
	_ = r.RecordInput("step 3")

	hits, _ := r.SearchFlight(FlightSearchOptions{Query: "anchor"})
	if len(hits) == 0 {
		t.Fatal("anchor not found")
	}
	anchorID := hits[0].ID

	timeline, err := r.TimelineFlight(anchorID, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Anchor at index 2 in chrono order; depth=2 gives indices 0..4
	// (full 5 entries). But we only have 5 entries total so all
	// should come back.
	if len(timeline) != 5 {
		t.Errorf("expected 5 entries in timeline, got %d", len(timeline))
	}
}
