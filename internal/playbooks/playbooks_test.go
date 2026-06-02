package playbooks

import (
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "playbooks"))
}

func basePlaybook() *Playbook {
	return &Playbook{
		Name:      "migration playbook",
		TaskTypes: []string{"migration", "schema"},
		Content:   "1. Plan the migration\n2. Test against staging\n3. Run with --dry-run first",
	}
}

func TestCreate_RejectsMissingFields(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		name string
		pb   *Playbook
	}{
		{"empty name", &Playbook{TaskTypes: []string{"x"}, Content: "y"}},
		{"empty content", &Playbook{Name: "x", TaskTypes: []string{"x"}}},
		{"no task types", &Playbook{Name: "x", Content: "y"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := s.Create(c.pb); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestCreate_AssignsIDAndTimestamps(t *testing.T) {
	s := newStore(t)
	pb, err := s.Create(basePlaybook())
	if err != nil {
		t.Fatal(err)
	}
	if pb.ID == "" || pb.CreatedAt.IsZero() || pb.UpdatedAt.IsZero() {
		t.Errorf("ID/timestamps not set: %+v", pb)
	}
}

func TestUse_BumpsCountAndLastUsedAt(t *testing.T) {
	s := newStore(t)
	pb, _ := s.Create(basePlaybook())
	updated, err := s.Use(pb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.UseCount != 1 {
		t.Errorf("UseCount should be 1, got %d", updated.UseCount)
	}
	if updated.LastUsedAt.IsZero() {
		t.Error("LastUsedAt should be set")
	}
}

func TestRecordOutcome_BumpsRightCounter(t *testing.T) {
	s := newStore(t)
	pb, _ := s.Create(basePlaybook())
	_, _ = s.RecordOutcome(pb.ID, true)
	_, _ = s.RecordOutcome(pb.ID, true)
	updated, _ := s.RecordOutcome(pb.ID, false)
	if updated.SuccessCount != 2 || updated.FailureCount != 1 {
		t.Errorf("outcomes off: success=%d failure=%d", updated.SuccessCount, updated.FailureCount)
	}
	rate := updated.SuccessRate()
	if rate < 0.66 || rate > 0.67 {
		t.Errorf("success rate ~0.667, got %v", rate)
	}
}

func TestSuccessRate_EmptyIsNeutralPrior(t *testing.T) {
	pb := &Playbook{}
	if pb.SuccessRate() != 0.5 {
		t.Errorf("untouched playbook should have neutral prior 0.5, got %v", pb.SuccessRate())
	}
}

func TestRefine_CreatesChildLinkingParent(t *testing.T) {
	s := newStore(t)
	parent, _ := s.Create(basePlaybook())
	_, _ = s.RecordOutcome(parent.ID, true) // parent has history
	child, err := s.Refine(parent.ID, "refined content", "tweaked the dry-run step")
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != parent.ID {
		t.Errorf("ParentID not linked: %s", child.ParentID)
	}
	if child.Content == parent.Content {
		t.Error("child content should differ from parent")
	}
	// Parent counters intact.
	freshParent, _ := s.Get(parent.ID)
	if freshParent.SuccessCount != 1 {
		t.Errorf("parent counters disturbed: %+v", freshParent)
	}
}

func TestRank_PrefersExactTaskTypeMatch(t *testing.T) {
	s := newStore(t)
	_, _ = s.Create(&Playbook{Name: "migration", TaskTypes: []string{"migration"}, Content: "x"})
	_, _ = s.Create(&Playbook{Name: "refactor", TaskTypes: []string{"refactor"}, Content: "x"})

	hits, err := s.Rank("migration", "", 5, RankOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) < 1 {
		t.Fatalf("expected at least 1 hit")
	}
	if hits[0].Playbook.Name != "migration" {
		t.Errorf("migration playbook should rank first, got %s", hits[0].Playbook.Name)
	}
}

func TestRank_PenalizesLowSuccessRate(t *testing.T) {
	s := newStore(t)
	winner, _ := s.Create(&Playbook{Name: "winner", TaskTypes: []string{"migration"}, Content: "x"})
	loser, _ := s.Create(&Playbook{Name: "loser", TaskTypes: []string{"migration"}, Content: "x"})

	// Winner has 4/5 success, loser has 1/5.
	for i := 0; i < 4; i++ {
		_, _ = s.RecordOutcome(winner.ID, true)
	}
	_, _ = s.RecordOutcome(winner.ID, false)
	_, _ = s.RecordOutcome(loser.ID, true)
	for i := 0; i < 4; i++ {
		_, _ = s.RecordOutcome(loser.ID, false)
	}

	hits, _ := s.Rank("migration", "", 5, RankOptions{})
	if len(hits) < 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Playbook.Name != "winner" {
		t.Errorf("higher success rate should rank first, got %s", hits[0].Playbook.Name)
	}
}

func TestRank_DropsZeroMatch(t *testing.T) {
	s := newStore(t)
	_, _ = s.Create(&Playbook{Name: "auth", TaskTypes: []string{"auth"}, Content: "x"})
	hits, _ := s.Rank("totally-unrelated", "", 5, RankOptions{})
	if len(hits) != 0 {
		t.Errorf("zero-match should drop everything, got %+v", hits)
	}
}

func TestRank_EmptyInputsReturnsRecencySorted(t *testing.T) {
	s := newStore(t)
	old, _ := s.Create(&Playbook{Name: "old", TaskTypes: []string{"x"}, Content: "x"})
	fresh, _ := s.Create(&Playbook{Name: "fresh", TaskTypes: []string{"x"}, Content: "x"})
	// Backdate the "old" playbook's LastUsedAt by 14 days.
	o, _ := s.Get(old.ID)
	o.LastUsedAt = time.Now().Add(-14 * 24 * time.Hour)
	_ = s.saveLocked(o)
	_, _ = s.Use(fresh.ID)

	hits, _ := s.Rank("", "", 5, RankOptions{})
	if len(hits) < 2 || hits[0].Playbook.ID != fresh.ID {
		t.Errorf("fresh should rank first on empty input, got %+v", hits)
	}
}

func TestRank_TopK(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 5; i++ {
		_, _ = s.Create(&Playbook{Name: "pb", TaskTypes: []string{"x"}, Content: "x"})
	}
	hits, _ := s.Rank("x", "", 2, RankOptions{})
	if len(hits) != 2 {
		t.Errorf("topK=2 should cap result, got %d", len(hits))
	}
}

func TestDelete_Idempotent(t *testing.T) {
	s := newStore(t)
	pb, _ := s.Create(basePlaybook())
	if err := s.Delete(pb.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(pb.ID); err != nil {
		t.Errorf("second delete should be no-op, got %v", err)
	}
}
