package tasks

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStore_OpenAssignsIDAndStatus(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	task, err := s.Open("sess-1", "fix auth bug")
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" {
		t.Error("ID should be assigned")
	}
	if task.Status != StatusOpen {
		t.Errorf("new task should be open, got %s", task.Status)
	}
}

func TestStore_OpenRequiresIntent(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	if _, err := s.Open("sess-1", "   "); err == nil {
		t.Error("empty intent should error")
	}
}

func TestStore_SetStatusStampsResolvedOnTerminal(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	task, _ := s.Open("sess-1", "fix auth")

	updated, err := s.SetStatus(task.ID, StatusShipped)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusShipped {
		t.Errorf("status not updated: %s", updated.Status)
	}
	if updated.ResolvedAt.IsZero() {
		t.Error("ResolvedAt should be stamped on terminal transition")
	}
}

func TestStore_SetStatusDoesNotStampResolvedOnInProgress(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	task, _ := s.Open("sess-1", "x")
	updated, _ := s.SetStatus(task.ID, StatusInProgress)
	if !updated.ResolvedAt.IsZero() {
		t.Error("non-terminal transition should not stamp ResolvedAt")
	}
}

func TestStore_LinkCommitIsIdempotent(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	task, _ := s.Open("sess-1", "x")
	_, _ = s.LinkCommit(task.ID, "abc123")
	updated, _ := s.LinkCommit(task.ID, "abc123")
	if len(updated.Commits) != 1 {
		t.Errorf("re-linking same SHA should be no-op, got %d commits", len(updated.Commits))
	}
}

func TestStore_LinkCommitMultipleAccumulates(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	task, _ := s.Open("sess-1", "x")
	_, _ = s.LinkCommit(task.ID, "abc")
	_, _ = s.LinkCommit(task.ID, "def")
	updated, _ := s.LinkCommit(task.ID, "ghi")
	if len(updated.Commits) != 3 {
		t.Errorf("expected 3 commits, got %d", len(updated.Commits))
	}
}

func TestStore_AppendNoteJoinsWithSeparator(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	task, _ := s.Open("sess-1", "x")
	_, _ = s.AppendNote(task.ID, "tried A, failed")
	updated, _ := s.AppendNote(task.ID, "tried B, worked")
	if !strings.Contains(updated.Notes, " | ") {
		t.Errorf("notes should be separated: %q", updated.Notes)
	}
	if !strings.Contains(updated.Notes, "failed") || !strings.Contains(updated.Notes, "worked") {
		t.Errorf("both notes should survive: %q", updated.Notes)
	}
}

func TestStore_OpenTasksFiltersTerminals(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	a, _ := s.Open("sess", "a")
	_, _ = s.Open("sess", "b")
	c, _ := s.Open("sess", "c")
	_, _ = s.SetStatus(a.ID, StatusShipped)
	_, _ = s.SetStatus(c.ID, StatusAbandoned)

	open, err := s.OpenTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Errorf("expected 1 open task, got %d", len(open))
	}
	if open[0].Intent != "b" {
		t.Errorf("wrong task returned: %+v", open[0])
	}
}

func TestStore_OpenOlderThanFiltersByAge(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	s := NewStore(dir)
	// Old task — synthesise by writing one directly with backdated
	// CreatedAt.
	old := &Task{
		ID:        "old",
		SessionID: "s",
		Intent:    "ancient request",
		Status:    StatusOpen,
		CreatedAt: time.Now().Add(-72 * time.Hour),
		UpdatedAt: time.Now().Add(-72 * time.Hour),
	}
	if err := s.saveLocked(old); err != nil {
		t.Fatal(err)
	}
	// Fresh task — Open() stamps it as now.
	_, _ = s.Open("sess", "fresh request")

	stale, err := s.OpenOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].ID != "old" {
		t.Errorf("expected only the old task, got %+v", stale)
	}
}

func TestStore_AllSortsNewestFirst(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	for _, intent := range []string{"first", "second", "third"} {
		_, _ = s.Open("sess", intent)
		time.Sleep(2 * time.Millisecond) // ensure distinct timestamps
	}
	all, _ := s.All()
	if len(all) != 3 || all[0].Intent != "third" {
		t.Errorf("expected newest-first ordering, got %+v", intentNames(all))
	}
}

func TestStore_SearchMatchesIntentAndNotes(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "tasks"))
	a, _ := s.Open("sess", "fix auth middleware")
	_, _ = s.AppendNote(a.ID, "csrf token unscoped")
	_, _ = s.Open("sess", "cache layer rewrite")

	hits, _ := s.Search("auth")
	if len(hits) != 1 || hits[0].Intent != "fix auth middleware" {
		t.Errorf("intent match failed: %+v", hits)
	}
	hits, _ = s.Search("csrf")
	if len(hits) != 1 {
		t.Errorf("note match failed: %+v", hits)
	}
}

func TestFormatOpenerSummary_EmptyReturnsEmpty(t *testing.T) {
	if got := FormatOpenerSummary(nil); got != "" {
		t.Errorf("empty list → empty string, got %q", got)
	}
}

func TestFormatOpenerSummary_RendersIntentAgeAndCommits(t *testing.T) {
	tasks := []*Task{
		{Intent: "fix auth", Status: StatusOpen, CreatedAt: time.Now().Add(-72 * time.Hour), Commits: []string{"abc1234"}},
		{Intent: "add csrf", Status: StatusInProgress, CreatedAt: time.Now().Add(-24 * time.Hour)},
	}
	got := FormatOpenerSummary(tasks)
	for _, want := range []string{"fix auth", "add csrf", "open", "in_progress", "abc1234"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q: %s", want, got)
		}
	}
}

func intentNames(ts []*Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Intent
	}
	return out
}
