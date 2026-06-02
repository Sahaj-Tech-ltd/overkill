package journal

import (
	"path/filepath"
	"testing"
	"time"
)

func mkReply(content string) Entry {
	return Entry{
		ID:        "e1",
		Type:      EntryAgentReply,
		SessionID: "s1",
		Timestamp: time.Now().UTC(),
		Content:   content,
	}
}

func TestExtractFailedHypotheses_TriedFailedBecause(t *testing.T) {
	e := mkReply("I tried bumping the timeout to 30s, but it failed because the upstream dropped the connection after 10s.")
	got := ExtractFailedHypotheses(e)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %d (%+v)", len(got), got)
	}
	if got[0].Hypothesis == "" || got[0].Reason == "" {
		t.Errorf("hypothesis/reason should be populated: %+v", got[0])
	}
}

func TestExtractFailedHypotheses_DidNotWorkBecause(t *testing.T) {
	e := mkReply("Caching the response did not work because the keys vary per-user.")
	got := ExtractFailedHypotheses(e)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %+v", got)
	}
}

func TestExtractFailedHypotheses_SubjectExtractedFromTheNoun(t *testing.T) {
	e := mkReply("I tried fixing the auth-middleware by reordering, but it failed because the session cookie was still null.")
	got := ExtractFailedHypotheses(e)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %+v", got)
	}
	if got[0].Subject == "" {
		t.Errorf("expected subject to be extracted, got empty: %+v", got[0])
	}
}

func TestExtractFailedHypotheses_NoFalsePositiveOnPlainSuccess(t *testing.T) {
	e := mkReply("I tried bumping the timeout and it worked perfectly.")
	if got := ExtractFailedHypotheses(e); len(got) != 0 {
		t.Errorf("plain success should not extract: %+v", got)
	}
}

func TestExtractFailedHypotheses_IgnoresNonAgentReply(t *testing.T) {
	e := Entry{
		Type:    EntryToolResult,
		Content: "I tried X, but it failed because Y.",
	}
	if got := ExtractFailedHypotheses(e); len(got) != 0 {
		t.Errorf("should only run on agent_reply: %+v", got)
	}
}

func TestExtractFailedHypotheses_DedupesWithinReply(t *testing.T) {
	e := mkReply("I tried the same patch twice — it failed because the file was read-only. I tried the same patch twice — it failed because the file was read-only.")
	got := ExtractFailedHypotheses(e)
	if len(got) > 1 {
		t.Errorf("expected dedup, got %d findings: %+v", len(got), got)
	}
}

func TestFailedHypothesisStore_AppendAndAll(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fh")
	s := NewFailedHypothesisStore(dir)

	h1 := FailedHypothesis{ID: "1", Hypothesis: "raise timeout", Reason: "upstream slow", Timestamp: time.Now().UTC()}
	h2 := FailedHypothesis{ID: "2", Hypothesis: "cache result", Reason: "keys vary", Timestamp: time.Now().UTC()}

	if err := s.Append(h1); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(h2); err != nil {
		t.Fatal(err)
	}

	all, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 records, got %d", len(all))
	}
}

func TestFailedHypothesisStore_SearchMatchesSubstring(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fh")
	s := NewFailedHypothesisStore(dir)
	_ = s.Append(FailedHypothesis{ID: "1", Subject: "auth", Hypothesis: "reorder middleware", Reason: "cookie was null", Timestamp: time.Now().UTC()})
	_ = s.Append(FailedHypothesis{ID: "2", Subject: "cache", Hypothesis: "add Redis layer", Reason: "miss rate too high", Timestamp: time.Now().UTC()})

	hits, err := s.Search("cookie")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "1" {
		t.Errorf("expected 1 hit for 'cookie', got %+v", hits)
	}

	hits, _ = s.Search("MIDDLEWARE") // case-insensitive
	if len(hits) != 1 {
		t.Errorf("expected case-insensitive match, got %+v", hits)
	}

	hits, _ = s.Search("")
	if len(hits) != 0 {
		t.Errorf("empty query should return nothing, got %+v", hits)
	}
}

func TestFailedHypothesisStore_AllOnMissingDir(t *testing.T) {
	s := NewFailedHypothesisStore(filepath.Join(t.TempDir(), "nope"))
	all, err := s.All()
	if err != nil {
		t.Errorf("missing dir should not error: %v", err)
	}
	if all != nil {
		t.Errorf("missing dir should return nil, got %+v", all)
	}
}
