package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
)

func newFHStore(t *testing.T, records ...journal.FailedHypothesis) *journal.FailedHypothesisStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "fh")
	s := journal.NewFailedHypothesisStore(dir)
	for _, r := range records {
		if r.Timestamp.IsZero() {
			r.Timestamp = time.Now().UTC()
		}
		if err := s.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestBuildRecalibrationProbe_EmptyWhenNoFingerprint(t *testing.T) {
	store := newFHStore(t)
	if got := buildRecalibrationProbe(store, nil, 5); got != "" {
		t.Errorf("nil prev should yield empty probe: %q", got)
	}
}

func TestBuildRecalibrationProbe_EmptyWhenNoPriorRecords(t *testing.T) {
	store := newFHStore(t, journal.FailedHypothesis{
		ModelID: "claude-opus-4-7", Subject: "auth", Hypothesis: "x", Reason: "y",
	})
	prev := &personality.ModelFingerprint{Family: "gpt", Version: "gpt-5-4"}
	if got := buildRecalibrationProbe(store, prev, 5); got != "" {
		t.Errorf("no matching prior records should yield empty: %q", got)
	}
}

func TestBuildRecalibrationProbe_GroupsBySubjectAndRanks(t *testing.T) {
	store := newFHStore(t,
		journal.FailedHypothesis{ModelID: "claude-opus-4-7", Subject: "auth", Hypothesis: "h", Reason: "r"},
		journal.FailedHypothesis{ModelID: "claude-opus-4-7", Subject: "auth", Hypothesis: "h2", Reason: "r2"},
		journal.FailedHypothesis{ModelID: "claude-opus-4-7", Subject: "auth", Hypothesis: "h3", Reason: "r3"},
		journal.FailedHypothesis{ModelID: "claude-opus-4-7", Subject: "cache", Hypothesis: "h", Reason: "r"},
		journal.FailedHypothesis{ModelID: "gpt-5-4", Subject: "ui", Hypothesis: "noise", Reason: "shouldn't appear"},
	)
	prev := &personality.ModelFingerprint{Family: "claude-opus", Version: "claude-opus-4-7"}
	probe := buildRecalibrationProbe(store, prev, 10)
	if probe == "" {
		t.Fatal("expected a probe")
	}
	// 4 prior failures across 2 subjects.
	if !strings.Contains(probe, "failed 4 time(s)") {
		t.Errorf("totalPrior count wrong: %s", probe)
	}
	if !strings.Contains(probe, "2 distinct area(s)") {
		t.Errorf("distinct count wrong: %s", probe)
	}
	// auth must appear first (3 > 1).
	idxAuth := strings.Index(probe, "auth")
	idxCache := strings.Index(probe, "cache")
	if idxAuth < 0 || idxCache < 0 || idxAuth >= idxCache {
		t.Errorf("auth should rank before cache: %s", probe)
	}
	// GPT noise must not appear.
	if strings.Contains(probe, "ui") {
		t.Errorf("noise from other model leaked: %s", probe)
	}
}

func TestBuildRecalibrationProbe_FamilyPrefixMatch(t *testing.T) {
	// Records tagged with a slightly different model string but
	// same family — should still be counted via prefix match.
	store := newFHStore(t,
		journal.FailedHypothesis{ModelID: "claude-opus-4-7-20260301", Subject: "auth", Hypothesis: "h", Reason: "r"},
	)
	prev := &personality.ModelFingerprint{Family: "claude-opus", Version: "claude-opus-4-7"}
	probe := buildRecalibrationProbe(store, prev, 10)
	if !strings.Contains(probe, "auth") {
		t.Errorf("family-prefix match should pick up the record: %s", probe)
	}
}

func TestBuildRecalibrationProbe_CapsToMaxItems(t *testing.T) {
	records := []journal.FailedHypothesis{}
	for i := 'a'; i <= 'j'; i++ {
		records = append(records, journal.FailedHypothesis{
			ModelID: "claude-opus-4-7", Subject: string(i), Hypothesis: "h", Reason: "r",
		})
	}
	store := newFHStore(t, records...)
	prev := &personality.ModelFingerprint{Family: "claude-opus", Version: "claude-opus-4-7"}
	probe := buildRecalibrationProbe(store, prev, 3)
	// 3 listed + "...and 7 more" continuation.
	if !strings.Contains(probe, "and 7 more") {
		t.Errorf("overflow continuation missing: %s", probe)
	}
}

func TestBelongsToPriorModel(t *testing.T) {
	cases := []struct {
		modelID, family, version string
		want                     bool
	}{
		{"claude-opus-4-7", "claude-opus", "claude-opus-4-7", true}, // version match
		{"claude-opus-4-7-20260301", "claude-opus", "", true},       // prefix match
		{"gpt-5-4", "claude-opus", "claude-opus-4-7", false},        // no match
		{"", "claude-opus", "x", false},                             // empty model
		{"claude-opus-4-7", "", "", false},                          // empty prev
	}
	for _, c := range cases {
		got := belongsToPriorModel(c.modelID, c.family, c.version)
		if got != c.want {
			t.Errorf("belongsToPriorModel(%q,%q,%q) = %v, want %v",
				c.modelID, c.family, c.version, got, c.want)
		}
	}
}
