package sidebar

import (
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
)

type stubProvider struct {
	p   *plan.Plan
	rem int
}

func (s stubProvider) Current() *plan.Plan { return s.p }
func (s stubProvider) Remaining() int      { return s.rem }

func TestTodoPanel_EmptyStateWhenNoProvider(t *testing.T) {
	p := NewTodoPanel(nil)
	out := p.View(40, 10)
	if !strings.Contains(out, "No active plan") {
		t.Errorf("expected empty-state copy: %q", out)
	}
}

func TestTodoPanel_RendersItemsWithCheckboxes(t *testing.T) {
	now := time.Now()
	pl := &plan.Plan{
		Title: "Fix bug",
		Items: []plan.Item{
			{ID: "1", Text: "reproduce", Done: true, DoneAt: &now},
			{ID: "2", Text: "patch"},
		},
		Updated: now,
	}
	p := NewTodoPanel(stubProvider{p: pl, rem: 1})
	out := p.View(60, 20)
	if !strings.Contains(out, "Fix bug") {
		t.Errorf("title missing: %q", out)
	}
	if !strings.Contains(out, "[x] reproduce") {
		t.Errorf("done item should render [x]: %q", out)
	}
	if !strings.Contains(out, "[ ] patch") {
		t.Errorf("pending item should render [ ]: %q", out)
	}
	if !strings.Contains(out, "1/2 done") {
		t.Errorf("counter missing: %q", out)
	}
}

func TestTodoPanel_PromptsForLearningWhenAllDone(t *testing.T) {
	pl := &plan.Plan{
		Items: []plan.Item{{ID: "1", Text: "a", Done: true}},
	}
	p := NewTodoPanel(stubProvider{p: pl, rem: 0})
	out := p.View(60, 20)
	if !strings.Contains(out, "record a learning") {
		t.Errorf("should prompt for learning when all done: %q", out)
	}
}

func TestTodoPanel_RendersNoteUnderItem(t *testing.T) {
	pl := &plan.Plan{
		Items: []plan.Item{{ID: "1", Text: "patch", Done: true, Note: "with caveat"}},
	}
	p := NewTodoPanel(stubProvider{p: pl, rem: 0})
	out := p.View(60, 20)
	if !strings.Contains(out, "with caveat") {
		t.Errorf("note should render: %q", out)
	}
}

func TestRelativeTime_Buckets(t *testing.T) {
	cases := []struct {
		offset   time.Duration
		contains string
	}{
		{time.Second, "just now"},
		{30 * time.Second, "s ago"},
		{30 * time.Minute, "m ago"},
		{5 * time.Hour, "h ago"},
	}
	for _, c := range cases {
		got := relativeTime(time.Now().Add(-c.offset))
		if !strings.Contains(got, c.contains) {
			t.Errorf("relativeTime(-%v) = %q, want substring %q", c.offset, got, c.contains)
		}
	}
}

func TestTruncate_ShortReturnsUnchanged(t *testing.T) {
	if truncateTodo("hi", 10) != "hi" {
		t.Error("short string should not be modified")
	}
	if got := truncateTodo("abcdefghij", 5); got != "abcd…" {
		t.Errorf("expected 'abcd…', got %q", got)
	}
}
