package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newSegmentStore(t *testing.T) (*SegmentStore, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(t.TempDir(), "segments")
	return NewSegmentStore(dir, root), root
}

func TestSegmentStore_CreateAssignsIDsAndTimestamps(t *testing.T) {
	s, _ := newSegmentStore(t)
	seg, err := s.Create(&Segment{Name: "auth", Globs: []string{"auth/*.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if seg.ID == "" || seg.CreatedAt.IsZero() {
		t.Errorf("ID + CreatedAt should be set: %+v", seg)
	}
}

func TestSegmentStore_CreateRejectsEmptyGlobs(t *testing.T) {
	s, _ := newSegmentStore(t)
	if _, err := s.Create(&Segment{Name: "x"}); err == nil {
		t.Error("empty globs should error")
	}
}

func TestSegmentStore_TouchBumpsAccessCountAndTime(t *testing.T) {
	s, _ := newSegmentStore(t)
	seg, _ := s.Create(&Segment{Name: "x", Globs: []string{"*.go"}})
	if err := s.Touch(seg.ID); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.Get(seg.ID)
	if updated.AccessCount != 1 {
		t.Errorf("AccessCount should be 1, got %d", updated.AccessCount)
	}
	if updated.LastAccessed.IsZero() {
		t.Error("LastAccessed should be stamped")
	}
}

func TestSegmentStore_LoadFilesResolvesGlobs(t *testing.T) {
	s, root := newSegmentStore(t)
	// Create some files to match.
	for _, name := range []string{"a.go", "b.go", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seg, _ := s.Create(&Segment{Name: "go-files", Globs: []string{"*.go"}})
	files, err := s.LoadFiles(seg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 .go files, got %d: %+v", len(files), files)
	}
	// Cache stats should be persisted.
	updated, _ := s.Get(seg.ID)
	if updated.CachedFileCount != 2 {
		t.Errorf("CachedFileCount should be 2, got %d", updated.CachedFileCount)
	}
	if updated.CachedTotalBytes <= 0 {
		t.Error("CachedTotalBytes should be positive")
	}
}

func TestSegmentStore_LoadFilesRecursiveGlob(t *testing.T) {
	s, root := newSegmentStore(t)
	// Nested layout.
	_ = os.MkdirAll(filepath.Join(root, "auth", "internal"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "auth", "auth.go"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "auth", "internal", "csrf.go"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "other", "thing.go"), []byte("x"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "other"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "other", "thing.go"), []byte("x"), 0o644)

	seg, _ := s.Create(&Segment{Name: "auth-deep", Globs: []string{"auth/**.go"}})
	files, err := s.LoadFiles(seg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files under auth/, got %d: %+v", len(files), files)
	}
}

func TestSegmentStore_RankPrefersMatch(t *testing.T) {
	s, _ := newSegmentStore(t)
	_, _ = s.Create(&Segment{Name: "auth", Globs: []string{"auth/*.go"}})
	_, _ = s.Create(&Segment{Name: "cache", Globs: []string{"cache/*.go"}})

	hits, err := s.Rank("auth", 5, RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("query 'auth' should match one segment, got %d", len(hits))
	}
	if hits[0].Segment.Name != "auth" {
		t.Errorf("wrong segment ranked first: %+v", hits[0].Segment)
	}
}

func TestSegmentStore_RankFavorsRecentOnEmptyQuery(t *testing.T) {
	s, _ := newSegmentStore(t)
	oldSeg, _ := s.Create(&Segment{Name: "old", Globs: []string{"*.go"}})
	freshSeg, _ := s.Create(&Segment{Name: "fresh", Globs: []string{"*.go"}})
	// Backdate the "old" segment's LastAccessed to 5 days ago.
	old, _ := s.Get(oldSeg.ID)
	old.LastAccessed = time.Now().Add(-120 * time.Hour)
	_ = s.saveLocked(old)
	_ = s.Touch(freshSeg.ID)

	hits, _ := s.Rank("", 2, RankOptions{})
	if len(hits) < 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Segment.ID != freshSeg.ID {
		t.Errorf("fresh segment should rank above old, got %+v then %+v", hits[0].Segment, hits[1].Segment)
	}
}

func TestSegmentStore_RankDropsZeroMatchOnQuery(t *testing.T) {
	s, _ := newSegmentStore(t)
	_, _ = s.Create(&Segment{Name: "auth", Globs: []string{"a/*.go"}})
	_, _ = s.Create(&Segment{Name: "cache", Globs: []string{"c/*.go"}})

	hits, _ := s.Rank("unrelated-query-xyz", 5, RankOptions{})
	if len(hits) != 0 {
		t.Errorf("query with no match should drop all candidates, got %+v", hits)
	}
}

func TestSegmentStore_RankLimitsToTopK(t *testing.T) {
	s, _ := newSegmentStore(t)
	for i := 0; i < 5; i++ {
		_, _ = s.Create(&Segment{Name: "seg", Globs: []string{"*.go"}})
	}
	hits, _ := s.Rank("", 2, RankOptions{})
	if len(hits) != 2 {
		t.Errorf("expected topK=2, got %d", len(hits))
	}
}

func TestSegmentStore_SearchByTag(t *testing.T) {
	s, _ := newSegmentStore(t)
	_, _ = s.Create(&Segment{Name: "x", Globs: []string{"*.go"}, Tags: []string{"backend", "auth"}})
	_, _ = s.Create(&Segment{Name: "y", Globs: []string{"*.go"}, Tags: []string{"frontend"}})

	hits, _ := s.Search("backend")
	if len(hits) != 1 || hits[0].Name != "x" {
		t.Errorf("tag search failed: %+v", hits)
	}
}

func TestSegmentStore_DeleteIsIdempotent(t *testing.T) {
	s, _ := newSegmentStore(t)
	seg, _ := s.Create(&Segment{Name: "x", Globs: []string{"*.go"}})
	if err := s.Delete(seg.ID); err != nil {
		t.Fatal(err)
	}
	// Second delete should not error.
	if err := s.Delete(seg.ID); err != nil {
		t.Errorf("second delete should be no-op, got %v", err)
	}
}

func TestSegmentStore_GetMissingReturnsNil(t *testing.T) {
	s, _ := newSegmentStore(t)
	seg, err := s.Get("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if seg != nil {
		t.Errorf("missing segment should return nil, got %+v", seg)
	}
}

func TestPow2_Boundaries(t *testing.T) {
	cases := map[float64]float64{
		0:   1,
		-1:  0.5,
		-2:  0.25,
		-50: 0,
	}
	for in, want := range cases {
		got := pow2(in)
		if got < want-0.01 || got > want+0.01 {
			t.Errorf("pow2(%v) = %v, want ~%v", in, got, want)
		}
	}
}
