package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type titleProvider struct {
	resp    string
	err     error
	lastReq providers.Request
}

func (p *titleProvider) Name() string              { return "fake" }
func (p *titleProvider) Models() []providers.Model { return nil }
func (p *titleProvider) Complete(_ context.Context, r providers.Request) (providers.Response, error) {
	p.lastReq = r
	if p.err != nil {
		return providers.Response{}, p.err
	}
	return providers.Response{Content: p.resp, Model: r.Model}, nil
}
func (p *titleProvider) Stream(_ context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
	return nil, errors.New("not implemented")
}

func TestGenerateTitle_NoProvider(t *testing.T) {
	a := &Agent{history: []providers.Message{{Role: "user", Content: "hi"}}}
	got, err := a.GenerateTitle(context.Background())
	if err != nil {
		t.Errorf("nil provider should not error: %v", err)
	}
	if got != "" {
		t.Errorf("nil provider should return empty, got %q", got)
	}
}

func TestGenerateTitle_EmptyHistory(t *testing.T) {
	a := &Agent{provider: &titleProvider{resp: "x"}}
	got, _ := a.GenerateTitle(context.Background())
	if got != "" {
		t.Errorf("empty history should return empty, got %q", got)
	}
}

func TestGenerateTitle_StripsQuotesAndPeriod(t *testing.T) {
	p := &titleProvider{resp: `  "Refactor the auth module."  `}
	a := &Agent{
		provider: p,
		history: []providers.Message{
			{Role: "user", Content: "fix auth"},
			{Role: "assistant", Content: "ok"},
		},
	}
	got, err := a.GenerateTitle(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Refactor the auth module" {
		t.Errorf("expected quotes+period stripped, got %q", got)
	}
	if p.lastReq.MaxTokens != 80 {
		t.Errorf("expected MaxTokens=80, got %d", p.lastReq.MaxTokens)
	}
}

func TestGenerateTitle_TruncatesLong(t *testing.T) {
	long := strings.Repeat("a", 200)
	a := &Agent{
		provider: &titleProvider{resp: long},
		history:  []providers.Message{{Role: "user", Content: "x"}},
	}
	got, _ := a.GenerateTitle(context.Background())
	if r := []rune(got); len(r) > 80 {
		t.Errorf("expected ≤80 runes, got %d", len(r))
	}
}

func TestGenerateTitle_ProviderErrorBubbles(t *testing.T) {
	a := &Agent{
		provider: &titleProvider{err: errors.New("provider down")},
		history:  []providers.Message{{Role: "user", Content: "x"}},
	}
	_, err := a.GenerateTitle(context.Background())
	if err == nil {
		t.Error("expected error to bubble for caller fallback")
	}
}
