package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type stubRewriter struct {
	calls atomic.Int32
	out   string
}

func (s *stubRewriter) RewritePrompt(_ context.Context, in string) (string, error) {
	s.calls.Add(1)
	if s.out == "" {
		return in, nil
	}
	return s.out, nil
}

func TestAgent_Rewriter_AppliedWhenSet(t *testing.T) {
	rw := &stubRewriter{out: "REWRITTEN"}
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			// Provider sees the rewritten content.
			last := req.Messages[len(req.Messages)-1]
			if !strings.Contains(last.Content, "REWRITTEN") {
				t.Errorf("provider did not see rewritten message: %q", last.Content)
			}
			return providers.Response{Model: req.Model, Content: "ok"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100, MaxSteps: 1})
	a.SetRewriter(rw)
	a.appendMessage(providers.Message{Role: "user", Content: "original prompt"})

	_ = a.buildRequest()
	if rw.calls.Load() == 0 {
		t.Fatal("expected rewriter to be invoked")
	}

	// H1: Rewriter now gets a defensive copy — history is preserved.
	// The rewritten content is used in the API request, not stored back.
	// Verify the rewriter was called (already checked above).
	hist := a.History()
	if len(hist) == 0 || hist[len(hist)-1].Content != "original prompt" {
		t.Errorf("expected history to preserve original content, got %v", hist)
	}
}

func TestAgent_Rewriter_BypassedWhenNil(t *testing.T) {
	rw := &stubRewriter{out: "REWRITTEN"}
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			return providers.Response{Model: req.Model, Content: "ok"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100, MaxSteps: 1})
	// No SetRewriter call — bypass.
	a.appendMessage(providers.Message{Role: "user", Content: "original prompt"})
	_ = a.buildRequest()

	if rw.calls.Load() != 0 {
		t.Fatal("expected rewriter NOT to be invoked when not set")
	}
	hist := a.History()
	if hist[len(hist)-1].Content != "original prompt" {
		t.Errorf("expected original prompt unchanged, got %q", hist[len(hist)-1].Content)
	}
}
