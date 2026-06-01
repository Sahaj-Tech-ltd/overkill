package tools

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
)

func newTagsManagerForTest(t *testing.T) *tags.Manager {
	t.Helper()
	m, err := tags.NewManager(filepath.Join(t.TempDir(), "tags.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type fakeJournalReader struct {
	entries map[string]*journal.Entry
	err     error
}

func (f fakeJournalReader) GetFlight(id string) (*journal.Entry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.entries[id], nil
}

func TestBookmarkCreate_MissingTags(t *testing.T) {
	tool := NewBookmarkCreateTool(nil)
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{"id":"x","label":"y"}`))
	if !strings.Contains(string(got), "not configured") {
		t.Errorf("expected not-configured, got %s", got)
	}
}

func TestBookmarkCreate_RequiresIDAndLabel(t *testing.T) {
	tool := NewBookmarkCreateTool(newTagsManagerForTest(t))
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(got), "id is required") {
		t.Errorf("expected id-required, got %s", got)
	}
	got, _ = tool.Execute(context.Background(), json.RawMessage(`{"id":"x"}`))
	if !strings.Contains(string(got), "label is required") {
		t.Errorf("expected label-required, got %s", got)
	}
}

func TestBookmarkCreate_TagsWithPrefix(t *testing.T) {
	m := newTagsManagerForTest(t)
	tool := NewBookmarkCreateTool(m)
	_, err := tool.Execute(context.Background(),
		json.RawMessage(`{"id":"entry-123","label":"Payment Bug","note":"the one from yesterday"}`))
	if err != nil {
		t.Fatal(err)
	}
	// Tag should be stored with the bookmark: prefix + slugged label.
	tagged := m.ByPath("entry-123")
	if len(tagged) != 1 {
		t.Fatalf("expected 1 tag on entry-123, got %d", len(tagged))
	}
	if tagged[0].Tag != "bookmark:payment-bug" {
		t.Errorf("unexpected tag: %q", tagged[0].Tag)
	}
	if tagged[0].Note != "the one from yesterday" {
		t.Errorf("note lost: %q", tagged[0].Note)
	}
}

func TestBookmarkList_FiltersToBookmarkPrefix(t *testing.T) {
	m := newTagsManagerForTest(t)
	// Mix bookmark + non-bookmark tags.
	_ = m.Tag("entry-1", "bookmark:first", "note one")
	_ = m.Tag("entry-2", "bookmark:second", "")
	_ = m.Tag("/repo/auth.go", "todo", "this isn't a bookmark")

	tool := NewBookmarkListTool(m)
	raw, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	var out struct {
		Bookmarks []map[string]string `json:"bookmarks"`
		Count     int                 `json:"count"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 2 {
		t.Errorf("expected 2 bookmarks (not the todo), got %d: %+v", out.Count, out.Bookmarks)
	}
}

func TestBookmarkRecall_MissingLabel(t *testing.T) {
	m := newTagsManagerForTest(t)
	tool := NewBookmarkRecallTool(m, fakeJournalReader{})
	got, _ := tool.Execute(context.Background(),
		json.RawMessage(`{"label":"nope"}`))
	if !strings.Contains(string(got), `no bookmark labelled`) || !strings.Contains(string(got), `nope`) {
		t.Errorf("expected not-found error, got %s", got)
	}
}

func TestBookmarkRecall_RoundTrip(t *testing.T) {
	m := newTagsManagerForTest(t)
	_ = m.Tag("entry-42", "bookmark:auth-fix", "fixed the redirect bug")

	reader := fakeJournalReader{entries: map[string]*journal.Entry{
		"entry-42": {
			ID:        "entry-42",
			Type:      journal.EntryAgentReply,
			Content:   "patched handleRedirect to validate origin",
			Timestamp: time.Now(),
		},
	}}
	tool := NewBookmarkRecallTool(m, reader)
	raw, _ := tool.Execute(context.Background(),
		json.RawMessage(`{"label":"auth-fix"}`))
	var out struct {
		Label string         `json:"label"`
		Entry *journal.Entry `json:"entry"`
		Note  string         `json:"note"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Label != "auth-fix" {
		t.Errorf("label = %q", out.Label)
	}
	if out.Entry == nil || !strings.Contains(out.Entry.Content, "handleRedirect") {
		t.Errorf("entry not surfaced: %+v", out.Entry)
	}
	if out.Note != "fixed the redirect bug" {
		t.Errorf("note not surfaced: %q", out.Note)
	}
}

func TestBookmarkRecall_DanglingPointer(t *testing.T) {
	m := newTagsManagerForTest(t)
	_ = m.Tag("ghost-entry", "bookmark:gone", "")
	reader := fakeJournalReader{entries: map[string]*journal.Entry{}}
	tool := NewBookmarkRecallTool(m, reader)
	got, _ := tool.Execute(context.Background(),
		json.RawMessage(`{"label":"gone"}`))
	if !strings.Contains(string(got), "missing") {
		t.Errorf("expected dangling-pointer error, got %s", got)
	}
}

func TestBookmarkRecall_JournalErrorSurfaces(t *testing.T) {
	m := newTagsManagerForTest(t)
	_ = m.Tag("e1", "bookmark:x", "")
	reader := fakeJournalReader{err: errors.New("journal disk full")}
	tool := NewBookmarkRecallTool(m, reader)
	got, _ := tool.Execute(context.Background(),
		json.RawMessage(`{"label":"x"}`))
	if !strings.Contains(string(got), "journal disk full") {
		t.Errorf("expected wrapped journal error, got %s", got)
	}
}
