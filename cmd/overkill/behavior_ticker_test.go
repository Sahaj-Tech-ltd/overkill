package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// withTempHome reroutes HOME so the ticker writes its outputs into
// a temp directory we can inspect. Restores the prior HOME on cleanup.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", prev) })
	if err := os.Setenv("HOME", dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeJournalEntry(t *testing.T, home string, e journal.Entry) {
	t.Helper()
	rawDir := filepath.Join(home, ".overkill", "journal", "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rawDir, e.Timestamp.Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, _ := json.Marshal(e)
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

func TestBehaviorTick_PersistsFailHypoAndAlerts(t *testing.T) {
	home := withTempHome(t)

	now := time.Now().UTC()
	// Two entries that should both fire:
	// - agent_reply with a failed-hypothesis shape
	// - agent_reply that claims success without any prior tool_result
	writeJournalEntry(t, home, journal.Entry{
		ID:        "e1",
		Type:      journal.EntryAgentReply,
		SessionID: "s1",
		Timestamp: now,
		Content:   "I tried bumping the timeout to 30s, but it failed because the upstream dropped early.",
	})
	writeJournalEntry(t, home, journal.Entry{
		ID:        "e2",
		Type:      journal.EntryAgentReply,
		SessionID: "s1",
		Timestamp: now.Add(time.Second),
		Content:   "All tests pass and the bug is fixed.",
	})

	// `since` set well before the entries so they're included.
	got := behaviorTick(now.Add(-time.Hour))
	if !got.After(now.Add(-time.Hour)) {
		t.Errorf("expected lastSeen to advance, got %v", got)
	}

	// failhypo store should now have one record.
	fhDir := filepath.Join(home, ".overkill", "failed_hypotheses")
	recs, err := journal.NewFailedHypothesisStore(fhDir).All()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Errorf("expected 1 failhypo record, got %d (%+v)", len(recs), recs)
	}

	// alert store should have at least one pattern_detected entry.
	alertDir := filepath.Join(home, ".overkill", "alerts")
	as := journal.NewAlertStore(alertDir)
	if err := as.Load(); err != nil {
		t.Fatal(err)
	}
	alerts := as.Pending()
	hits := 0
	for _, a := range alerts {
		if a.Type == journal.AlertPatternDetected {
			hits++
		}
	}
	if hits == 0 {
		t.Errorf("expected at least one pattern_detected alert, got %+v", alerts)
	}
}

func TestBehaviorTick_NoEntriesIsNoop(t *testing.T) {
	home := withTempHome(t)
	_ = home

	since := time.Now().Add(-time.Hour)
	got := behaviorTick(since)
	if !got.Equal(since) {
		t.Errorf("empty journal should leave lastSeen unchanged, got %v vs %v", got, since)
	}
}

func TestBehaviorTick_SkipsEntriesAtOrBeforeSince(t *testing.T) {
	home := withTempHome(t)

	old := time.Now().Add(-30 * time.Minute).UTC()
	writeJournalEntry(t, home, journal.Entry{
		ID:        "e1",
		Type:      journal.EntryAgentReply,
		SessionID: "s1",
		Timestamp: old,
		Content:   "I tried X but it failed because Y was missing.",
	})

	// since is AFTER the entry — should be skipped.
	since := time.Now()
	got := behaviorTick(since)
	if !got.Equal(since) {
		t.Errorf("entries before `since` should be skipped, got %v vs %v", got, since)
	}

	fhDir := filepath.Join(home, ".overkill", "failed_hypotheses")
	recs, _ := journal.NewFailedHypothesisStore(fhDir).All()
	if len(recs) != 0 {
		t.Errorf("skipped entry should not produce records, got %+v", recs)
	}
}
