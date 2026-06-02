package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRetriever struct {
	hits []MemoryHit
	err  error
}

func (f *fakeRetriever) Search(_ context.Context, _ string, _ int) ([]MemoryHit, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.hits, nil
}

func TestRenderMemorySection_NoRetriever(t *testing.T) {
	a := &Agent{}
	if got := a.renderMemorySection(context.Background(), "hello"); got != "" {
		t.Errorf("renderMemorySection without retriever = %q, want empty", got)
	}
}

func TestRenderMemorySection_EmptyInput(t *testing.T) {
	a := &Agent{}
	a.SetMemoryRetriever(&fakeRetriever{hits: []MemoryHit{{ID: "1", Text: "anything", Score: 1.0}}})
	if got := a.renderMemorySection(context.Background(), "   "); got != "" {
		t.Errorf("renderMemorySection with empty input = %q, want empty", got)
	}
}

func TestRenderMemorySection_ThreeHits(t *testing.T) {
	a := &Agent{}
	a.SetMemoryRetriever(&fakeRetriever{hits: []MemoryHit{
		{ID: "a", Text: "user prefers dark mode", Score: 0.91},
		{ID: "b", Text: "deploy via docker compose", Score: 0.82},
		{ID: "c", Text: "main branch is master", Score: 0.77},
	}})
	got := a.renderMemorySection(context.Background(), "how do I deploy")
	if got == "" {
		t.Fatal("expected non-empty memory section")
	}
	for _, want := range []string{
		"REFERENCE",
		"begin memories",
		"end memories",
		"user prefers dark mode",
		"deploy via docker compose",
		"main branch is master",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("memory section missing %q\n---got---\n%s", want, got)
		}
	}
}

func TestRenderMemorySection_ErrorReturnsEmpty(t *testing.T) {
	a := &Agent{}
	a.SetMemoryRetriever(&fakeRetriever{err: errors.New("boom")})
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("renderMemorySection panicked on retriever error: %v", r)
		}
	}()
	if got := a.renderMemorySection(context.Background(), "hello"); got != "" {
		t.Errorf("renderMemorySection on error = %q, want empty", got)
	}
}

func TestRenderMemorySection_LongHitTruncated(t *testing.T) {
	a := &Agent{}
	long := strings.Repeat("x", memoryExcerptMax+200)
	a.SetMemoryRetriever(&fakeRetriever{hits: []MemoryHit{
		{ID: "long", Text: long, Score: 0.5},
	}})
	got := a.renderMemorySection(context.Background(), "anything")
	if got == "" {
		t.Fatal("expected non-empty section")
	}
	if !strings.Contains(got, "...") {
		t.Errorf("expected truncation ellipsis, got:\n%s", got)
	}
	// Hard upper-bound: the rendered body shouldn't contain the full original
	// length (memoryExcerptMax+200 of "x" in a row).
	if strings.Contains(got, strings.Repeat("x", memoryExcerptMax+1)) {
		t.Errorf("expected truncation to bound at %d runes; got longer run", memoryExcerptMax)
	}
}

func TestRenderMemorySection_NilClears(t *testing.T) {
	a := &Agent{}
	a.SetMemoryRetriever(&fakeRetriever{hits: []MemoryHit{{Text: "x"}}})
	if a.renderMemorySection(context.Background(), "q") == "" {
		t.Fatal("expected output with retriever installed")
	}
	a.SetMemoryRetriever(nil)
	if got := a.renderMemorySection(context.Background(), "q"); got != "" {
		t.Errorf("renderMemorySection after nil clear = %q, want empty", got)
	}
}
