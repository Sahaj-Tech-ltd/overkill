package personality

import (
	"path/filepath"
	"testing"
)

func TestStyleInferencer_SaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "style.json")

	si := NewStyleInferencer()
	si.shortTerm = &WorkingStyle{
		Communication:  CommVerbose,
		ResponseExpect: RespCritique,
		Approach:       ApproachPlansFirst,
		DomainTerms:    []string{"agent", "context"},
	}
	si.sessionCount = 3
	si.lastCommittedStyle = &WorkingStyle{
		Communication:  CommDirect,
		ResponseExpect: RespAction,
		Approach:       ApproachDiveIn,
	}

	if err := si.SaveToFile(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	si2 := NewStyleInferencer()
	if err := si2.LoadFromFile(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if si2.sessionCount != 3 {
		t.Errorf("streak: got %d want 3", si2.sessionCount)
	}
	if si2.lastCommittedStyle == nil {
		t.Fatal("lastCommittedStyle nil after load")
	}
	// SaveToFile records shortTerm as LastSession so the next boot's
	// ConsecutiveSessionCommit can compare against this session's view.
	if si2.lastCommittedStyle.Communication != CommVerbose {
		t.Errorf("lastCommitted comm: got %v want verbose", si2.lastCommittedStyle.Communication)
	}
}

func TestStyleInferencer_LoadMissingFile(t *testing.T) {
	si := NewStyleInferencer()
	if err := si.LoadFromFile(filepath.Join(t.TempDir(), "nonexistent.json")); err != nil {
		t.Errorf("missing file should be no-op, got: %v", err)
	}
}

func TestStyleInferencer_LoadEmptyPath(t *testing.T) {
	si := NewStyleInferencer()
	if err := si.LoadFromFile(""); err != nil {
		t.Errorf("empty path should be no-op, got: %v", err)
	}
	if err := si.SaveToFile(""); err != nil {
		t.Errorf("empty path save should be no-op, got: %v", err)
	}
}

func TestConsecutiveSessionCommit_StreakIncrements(t *testing.T) {
	si := NewStyleInferencer()
	style := &WorkingStyle{
		Communication:  CommDirect,
		ResponseExpect: RespAction,
		Approach:       ApproachDiveIn,
	}
	si.shortTerm = style
	si.lastCommittedStyle = &WorkingStyle{
		Communication:  CommDirect,
		ResponseExpect: RespAction,
		Approach:       ApproachDiveIn,
	}
	si.sessionCount = 2

	promoted := si.ConsecutiveSessionCommit()
	if promoted {
		t.Error("should not promote at streak 3")
	}
	if si.sessionCount != 3 {
		t.Errorf("streak after match: got %d want 3", si.sessionCount)
	}
}

func TestConsecutiveSessionCommit_PromotesAtFive(t *testing.T) {
	si := NewStyleInferencer()
	matched := &WorkingStyle{
		Communication:  CommVerbose,
		ResponseExpect: RespCritique,
		Approach:       ApproachPlansFirst,
	}
	si.shortTerm = matched
	prev := *matched
	si.lastCommittedStyle = &prev
	si.sessionCount = 4

	promoted := si.ConsecutiveSessionCommit()
	if !promoted {
		t.Fatal("expected promotion at streak 5")
	}
	if si.sessionCount != 0 {
		t.Errorf("streak after promote: got %d want 0", si.sessionCount)
	}
	if si.baseline.Communication != CommVerbose {
		t.Errorf("baseline not updated: got %v", si.baseline.Communication)
	}
	if si.baseline.Approach != ApproachPlansFirst {
		t.Errorf("baseline approach not updated: got %v", si.baseline.Approach)
	}
}

func TestConsecutiveSessionCommit_DivergenceResets(t *testing.T) {
	si := NewStyleInferencer()
	si.shortTerm = &WorkingStyle{
		Communication:  CommVerbose,
		ResponseExpect: RespCritique,
		Approach:       ApproachPlansFirst,
	}
	si.lastCommittedStyle = &WorkingStyle{
		Communication:  CommDirect,
		ResponseExpect: RespAction,
		Approach:       ApproachDiveIn,
	}
	si.sessionCount = 4

	promoted := si.ConsecutiveSessionCommit()
	if promoted {
		t.Error("should not promote on divergence")
	}
	if si.sessionCount != 1 {
		t.Errorf("streak after divergence: got %d want 1 (fresh start)", si.sessionCount)
	}
}

func TestConsecutiveSessionCommit_FirstRunUsesBaseline(t *testing.T) {
	si := NewStyleInferencer()
	si.shortTerm = &WorkingStyle{
		Communication:  CommDirect,
		ResponseExpect: RespAction,
		Approach:       ApproachDiveIn,
	}
	si.lastCommittedStyle = nil
	si.sessionCount = 0

	si.ConsecutiveSessionCommit()
	if si.sessionCount != 1 {
		t.Errorf("first match against baseline: got %d want 1", si.sessionCount)
	}
	if si.lastCommittedStyle == nil {
		t.Fatal("lastCommittedStyle should be set after commit")
	}
}

func TestStylesMatch(t *testing.T) {
	base := &WorkingStyle{
		Communication:  CommDirect,
		ResponseExpect: RespAction,
		Approach:       ApproachDiveIn,
	}
	tests := []struct {
		name string
		a, b *WorkingStyle
		want bool
	}{
		{"identical", base, base, true},
		{"nil a", nil, base, false},
		{"nil b", base, nil, false},
		{"diff communication", base, &WorkingStyle{CommVerbose, RespAction, "", ApproachDiveIn, nil}, false},
		{"diff response", base, &WorkingStyle{CommDirect, RespCritique, "", ApproachDiveIn, nil}, false},
		{"diff approach", base, &WorkingStyle{CommDirect, RespAction, "", ApproachPlansFirst, nil}, false},
		{"frustration ignored", base, &WorkingStyle{CommDirect, RespAction, "!", ApproachDiveIn, nil}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stylesMatch(tt.a, tt.b); got != tt.want {
				t.Errorf("stylesMatch: got %v want %v", got, tt.want)
			}
		})
	}
}

func TestConsecutiveSessionCommit_NilSafe(t *testing.T) {
	var si *StyleInferencer
	if promoted := si.ConsecutiveSessionCommit(); promoted {
		t.Error("nil receiver should return false")
	}

	si2 := NewStyleInferencer()
	si2.shortTerm = nil
	if promoted := si2.ConsecutiveSessionCommit(); promoted {
		t.Error("nil shortTerm should return false")
	}
}
