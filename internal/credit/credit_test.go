package credit

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func newRec(id string, outcome Outcome, tags ...string) SessionRecord {
	actions := make([]Action, len(tags))
	for i, tag := range tags {
		actions[i] = Action{Tag: tag, Category: "tool"}
	}
	return SessionRecord{SessionID: id, Outcome: outcome, Actions: actions}
}

func TestAnalyzer_FoldSkipsUnknownOutcome(t *testing.T) {
	a := NewAnalyzer()
	a.Fold(newRec("u", OutcomeUnknown, "Read"))
	stats := a.Compute()
	if len(stats) != 0 {
		t.Errorf("unknown outcome should not be folded: %+v", stats)
	}
}

func TestAnalyzer_FoldOneSessionPerAction(t *testing.T) {
	a := NewAnalyzer()
	// Same action called 3 times in one session — should only
	// bump once per session.
	a.Fold(newRec("s1", OutcomeSuccess, "Read", "Read", "Read"))
	stats := a.Compute()
	if len(stats) != 1 {
		t.Fatalf("expected 1 action stat, got %d", len(stats))
	}
	if stats[0].SuccessCount != 1 || stats[0].TotalSessions != 1 {
		t.Errorf("dedup-per-session failed: %+v", stats[0])
	}
}

func TestAnalyzer_LiftAboveOneForCorrelatedSuccess(t *testing.T) {
	a := NewAnalyzer()
	// 5 successes that used Read, 5 failures that didn't.
	for i := 0; i < 5; i++ {
		a.Fold(newRec("s"+itoa(i), OutcomeSuccess, "Read"))
	}
	for i := 0; i < 5; i++ {
		a.Fold(newRec("f"+itoa(i), OutcomeFailure, "OtherTool"))
	}
	stats := a.Compute()
	var read ActionStats
	for _, s := range stats {
		if s.Tag == "Read" {
			read = s
			break
		}
	}
	if math.IsNaN(read.Lift) || read.Lift <= 1.0 {
		t.Errorf("Read should be lifted above 1.0, got %v", read.Lift)
	}
}

func TestAnalyzer_LiftBelowOneForCorrelatedFailure(t *testing.T) {
	a := NewAnalyzer()
	// 5 sessions where BadTool appears and all fail.
	for i := 0; i < 5; i++ {
		a.Fold(newRec("f"+itoa(i), OutcomeFailure, "BadTool"))
	}
	// 5 successful sessions WITHOUT BadTool (so without-baseline
	// has signal).
	for i := 0; i < 5; i++ {
		a.Fold(newRec("s"+itoa(i), OutcomeSuccess, "GoodTool"))
	}
	stats := a.Compute()
	var bad ActionStats
	for _, s := range stats {
		if s.Tag == "BadTool" {
			bad = s
			break
		}
	}
	if math.IsNaN(bad.Lift) || bad.Lift >= 1.0 {
		t.Errorf("BadTool should be lifted below 1.0, got %v", bad.Lift)
	}
}

func TestAnalyzer_ConfidenceBuckets(t *testing.T) {
	a := NewAnalyzer()
	// Low confidence: 1 session.
	a.Fold(newRec("a", OutcomeSuccess, "rare"))
	// Medium: 5 sessions.
	for i := 0; i < 5; i++ {
		a.Fold(newRec("m"+itoa(i), OutcomeSuccess, "medium"))
	}
	// High: 20 sessions.
	for i := 0; i < 20; i++ {
		a.Fold(newRec("h"+itoa(i), OutcomeSuccess, "common"))
	}
	stats := a.Compute()
	buckets := map[string]string{}
	for _, s := range stats {
		buckets[s.Tag] = s.Confidence
	}
	if buckets["rare"] != "low" || buckets["medium"] != "medium" || buckets["common"] != "high" {
		t.Errorf("confidence buckets wrong: %+v", buckets)
	}
}

func TestAnalyzer_SortsByAbsLiftDistance(t *testing.T) {
	a := NewAnalyzer()
	// "winner" → all 5 successes.
	for i := 0; i < 5; i++ {
		a.Fold(newRec("s"+itoa(i), OutcomeSuccess, "winner"))
	}
	// "loser" → all 5 failures.
	for i := 0; i < 5; i++ {
		a.Fold(newRec("f"+itoa(i), OutcomeFailure, "loser"))
	}
	// "neutral" → split.
	for i := 0; i < 5; i++ {
		a.Fold(newRec("n_s"+itoa(i), OutcomeSuccess, "neutral"))
	}
	for i := 0; i < 5; i++ {
		a.Fold(newRec("n_f"+itoa(i), OutcomeFailure, "neutral"))
	}
	stats := a.Compute()
	// First two entries should be the extremes (winner / loser),
	// not the neutral one.
	if len(stats) < 3 {
		t.Fatalf("expected 3 stats, got %d", len(stats))
	}
	top := stats[0].Tag
	if top != "winner" && top != "loser" {
		t.Errorf("top should be an extreme, got %s", top)
	}
}

func TestAnalyzer_SuccessCorrelated(t *testing.T) {
	a := NewAnalyzer()
	// "winner" appears in 5 successes, 0 failures → strong lift.
	for i := 0; i < 5; i++ {
		a.Fold(newRec("s"+itoa(i), OutcomeSuccess, "winner"))
	}
	for i := 0; i < 5; i++ {
		a.Fold(newRec("f"+itoa(i), OutcomeFailure, "OtherTool"))
	}
	hits := a.SuccessCorrelated(1.2, "medium")
	found := false
	for _, h := range hits {
		if h.Tag == "winner" {
			found = true
		}
	}
	if !found {
		t.Errorf("winner should appear in SuccessCorrelated, got %+v", hits)
	}
}

func TestAnalyzer_FailureCorrelated(t *testing.T) {
	a := NewAnalyzer()
	for i := 0; i < 5; i++ {
		a.Fold(newRec("f"+itoa(i), OutcomeFailure, "loser"))
	}
	for i := 0; i < 5; i++ {
		a.Fold(newRec("s"+itoa(i), OutcomeSuccess, "other"))
	}
	hits := a.FailureCorrelated(0.8, "medium")
	found := false
	for _, h := range hits {
		if h.Tag == "loser" {
			found = true
		}
	}
	if !found {
		t.Errorf("loser should appear in FailureCorrelated, got %+v", hits)
	}
}

func TestStore_SaveLoadRoundtrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "credit")
	s := NewStore(dir)
	rec := newRec("sess-1", OutcomeSuccess, "Read", "Write")
	if err := s.SaveSession(rec); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 record, got %d", len(loaded))
	}
	if len(loaded[0].Actions) != 2 {
		t.Errorf("actions not preserved: %+v", loaded[0])
	}
}

func TestStore_SaveRejectsEmptyID(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.SaveSession(SessionRecord{Outcome: OutcomeSuccess}); err == nil {
		t.Error("empty session id should error")
	}
}

func TestStore_LoadAllMissingIsNil(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "missing"))
	out, err := s.LoadAll()
	if err != nil {
		t.Errorf("missing dir should not error: %v", err)
	}
	if out != nil {
		t.Errorf("missing dir should return nil, got %+v", out)
	}
}

func TestFormatActionStats_RendersAxes(t *testing.T) {
	got := FormatActionStats(ActionStats{
		Tag:           "Read",
		Category:      "tool",
		SuccessCount:  18,
		TotalSessions: 22,
		Lift:          1.34,
		Confidence:    "medium",
	})
	for _, want := range []string{"tool/Read", "lift 1.34", "success 18/22", "confidence medium"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestFormatTopFindings_TruncatesToN(t *testing.T) {
	stats := []ActionStats{
		{Tag: "a", Lift: 2.0}, {Tag: "b", Lift: 1.5}, {Tag: "c", Lift: 1.2},
	}
	got := FormatTopFindings(stats, 2)
	if strings.Contains(got, "/c:") {
		t.Errorf("third entry should be truncated: %s", got)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	return out
}
