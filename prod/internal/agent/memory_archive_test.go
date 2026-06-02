package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type fakeArchiver struct {
	mu    sync.Mutex
	calls []archiveCall
	err   error
}

type archiveCall struct {
	SessionID, Role, Content string
}

func (f *fakeArchiver) Archive(_ context.Context, sessionID, role, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, archiveCall{sessionID, role, content})
	return f.err
}

func (f *fakeArchiver) seen() []archiveCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]archiveCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestArchiveCompactedMessages_NoArchiverIsNoOp(t *testing.T) {
	a := &Agent{}
	a.archiveCompactedMessages([]providers.Message{
		{Role: "user", Content: "hello world this is long enough to qualify"},
	})
	// Must not panic. Nothing to assert.
}

func TestArchiveCompactedMessages_ArchivesQualifyingMessages(t *testing.T) {
	ar := &fakeArchiver{}
	a := &Agent{_sessionID: "sess1"}
	a.SetMemoryArchiver(ar)

	hist := []providers.Message{
		{Role: "user", Content: "this is a user message of sufficient length to qualify"},
		{Role: "assistant", Content: "this is an assistant response of sufficient length"},
		{Role: "tool", Content: "this is a tool message that should NOT be archived"},
		{Role: "user", Content: "ok"}, // too short
		{Role: "assistant", Content: "[compacted history] prior summary placeholder"},
	}
	a.archiveCompactedMessages(hist)

	got := ar.seen()
	if len(got) != 2 {
		t.Fatalf("expected 2 archived messages, got %d", len(got))
	}
	if got[0].Role != "user" {
		t.Errorf("first archive should be user, got %s", got[0].Role)
	}
	if got[1].Role != "assistant" {
		t.Errorf("second archive should be assistant, got %s", got[1].Role)
	}
	if got[0].SessionID != "sess1" || got[1].SessionID != "sess1" {
		t.Errorf("session ID not propagated: %+v", got)
	}
}

func TestArchiveCompactedMessages_ErrorIsBestEffort(t *testing.T) {
	ar := &fakeArchiver{err: errors.New("vector store down")}
	a := &Agent{_sessionID: "sess1"}
	a.SetMemoryArchiver(ar)

	// Should not panic / not block. Errors are emitted via the
	// agent's event bus; we just verify it returns cleanly.
	a.archiveCompactedMessages([]providers.Message{
		{Role: "user", Content: "long enough message that should be archived even though it will fail"},
	})

	got := ar.seen()
	if len(got) != 1 {
		t.Errorf("archiver still receives the call even when it errors, got %d", len(got))
	}
}

func TestArchiveCompactedMessages_SkipsShort(t *testing.T) {
	ar := &fakeArchiver{}
	a := &Agent{}
	a.SetMemoryArchiver(ar)

	a.archiveCompactedMessages([]providers.Message{
		{Role: "user", Content: "tiny"},
		{Role: "user", Content: "      "}, // whitespace-only
		{Role: "user", Content: ""},
	})

	if got := ar.seen(); len(got) != 0 {
		t.Errorf("short messages should be skipped, got %v", got)
	}
}

func TestArchiveCompactedMessages_SkipsCompactionSummaries(t *testing.T) {
	ar := &fakeArchiver{}
	a := &Agent{}
	a.SetMemoryArchiver(ar)
	a.archiveCompactedMessages([]providers.Message{
		{Role: "assistant", Content: "[compacted history] some summary text that's long"},
	})
	if got := ar.seen(); len(got) != 0 {
		t.Errorf("compacted-history markers should not be re-archived, got %v", got)
	}
}
