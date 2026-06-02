package journal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// stubProvider returns canned content so the diary renderer tests
// don't depend on a live model. Captures the last system prompt so
// the test can assert the diary-shaped system prompt was used.
type stubProvider struct {
	content   string
	lastReq   providers.Request
	callCount int
}

func (s *stubProvider) Complete(ctx context.Context, req providers.Request) (providers.Response, error) {
	s.lastReq = req
	s.callCount++
	return providers.Response{Content: s.content}, nil
}

// Stream is unused by Summarizer but required to satisfy the provider
// interface — a minimal no-op suffices.
func (s *stubProvider) Stream(ctx context.Context, req providers.Request) (<-chan providers.Chunk, error) {
	ch := make(chan providers.Chunk)
	close(ch)
	return ch, nil
}

func (s *stubProvider) Name() string              { return "stub" }
func (s *stubProvider) Models() []providers.Model { return nil }

func TestNarrateSession_EmptyJournalIsNoOp(t *testing.T) {
	dir := t.TempDir()
	rec := NewFlightRecorder(dir, "sess-1")
	prov := &stubProvider{content: "should not be called"}
	s := NewSummarizer(rec, prov, "test-model")

	path, narr, err := s.NarrateSession(context.Background(), dir, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" || narr != "" {
		t.Errorf("empty session should return zero-value: path=%q narr=%q", path, narr)
	}
	if prov.callCount != 0 {
		t.Errorf("LLM should not be called for empty session, got %d calls", prov.callCount)
	}
}

func TestNarrateSession_WritesDayFile(t *testing.T) {
	dir := t.TempDir()
	rec := NewFlightRecorder(dir, "sess-1")
	_ = rec.RecordInput("fix the cache bug")
	_ = rec.RecordReply("done")

	prov := &stubProvider{content: "# 5/14\n\n## What we did\nFixed the cache bug.\n"}
	s := NewSummarizer(rec, prov, "test-model")

	path, narr, err := s.NarrateSession(context.Background(), dir, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Fatal("expected non-empty path")
	}
	if !strings.HasSuffix(path, ".md") {
		t.Errorf("path should be .md, got %s", path)
	}
	if !strings.Contains(narr, "Fixed the cache bug") {
		t.Errorf("narrative not propagated: %q", narr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Fixed the cache bug") {
		t.Errorf("file should contain narrative: %s", string(data))
	}
}

func TestNarrateSession_AppendsToExistingDayFile(t *testing.T) {
	dir := t.TempDir()
	when := time.Now().UTC()
	entriesDir := filepath.Join(dir, "entries")
	_ = os.MkdirAll(entriesDir, 0o750)
	existing := filepath.Join(entriesDir, when.Format("2006-01-02")+".md")
	_ = os.WriteFile(existing, []byte("# previous session content\n"), 0o600)

	rec := NewFlightRecorder(dir, "sess-2")
	_ = rec.RecordInput("new session work")
	prov := &stubProvider{content: "# 5/14\n\nNew session narrative."}
	s := NewSummarizer(rec, prov, "test-model")

	_, _, err := s.NarrateSession(context.Background(), dir, "sess-2")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(existing)
	body := string(data)
	if !strings.Contains(body, "previous session content") {
		t.Errorf("existing content should survive: %s", body)
	}
	if !strings.Contains(body, "New session narrative") {
		t.Errorf("new narrative should be appended: %s", body)
	}
	if !strings.Contains(body, "session sess-2") {
		t.Errorf("session attribution header missing: %s", body)
	}
}

func TestNarrateSession_UsesDiaryPrompt(t *testing.T) {
	dir := t.TempDir()
	rec := NewFlightRecorder(dir, "sess-3")
	_ = rec.RecordInput("anything")
	prov := &stubProvider{content: "ok"}
	s := NewSummarizer(rec, prov, "test-model")

	_, _, err := s.NarrateSession(context.Background(), dir, "sess-3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prov.lastReq.SystemPrompt, "work-journal sub-agent") {
		t.Errorf("expected diary system prompt, got: %s", prov.lastReq.SystemPrompt)
	}
	if !strings.Contains(prov.lastReq.SystemPrompt, "What we did") {
		t.Errorf("structured-sections prompt missing: %s", prov.lastReq.SystemPrompt)
	}
}
