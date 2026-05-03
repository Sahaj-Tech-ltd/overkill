package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type stubProvider struct {
	respond func(req providers.Request) (providers.Response, error)
}

func (s *stubProvider) Name() string { return "stub" }
func (s *stubProvider) Complete(ctx context.Context, req providers.Request) (providers.Response, error) {
	return s.respond(req)
}
func (s *stubProvider) Stream(ctx context.Context, req providers.Request) (<-chan providers.Chunk, error) {
	return nil, errors.New("not implemented")
}
func (s *stubProvider) Models() []providers.Model { return nil }

func TestRunVariantsParallel(t *testing.T) {
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			time.Sleep(20 * time.Millisecond)
			return providers.Response{
				Model:   req.Model,
				Content: "echo:" + req.Model,
				Usage:   providers.Usage{InputTokens: 5, OutputTokens: 10},
			}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100})
	start := time.Now()
	res, err := a.RunVariants(context.Background(), "hi", []string{"m1", "m2", "m3"})
	if err != nil {
		t.Fatal(err)
	}
	dur := time.Since(start)
	if dur > 100*time.Millisecond {
		t.Errorf("variants ran sequentially: %v", dur)
	}
	if len(res) != 3 {
		t.Fatalf("want 3 results")
	}
	for i, r := range res {
		if r.Response == "" {
			t.Errorf("variant %d empty", i)
		}
		if r.Tokens != 15 {
			t.Errorf("variant %d tokens=%d", i, r.Tokens)
		}
	}
}

func TestRunVariantsErrorPerVariant(t *testing.T) {
	prov := &stubProvider{
		respond: func(req providers.Request) (providers.Response, error) {
			if req.Model == "bad" {
				return providers.Response{}, errors.New("boom")
			}
			return providers.Response{Model: req.Model, Content: "ok"}, nil
		},
	}
	a := New(Config{Provider: prov, Model: "x", MaxTokens: 100})
	res, _ := a.RunVariants(context.Background(), "hi", []string{"good", "bad"})
	if res[0].Err != "" {
		t.Errorf("good variant errored: %v", res[0].Err)
	}
	if res[1].Err == "" {
		t.Errorf("bad variant should have erred")
	}
}

func TestRunVariantsEmpty(t *testing.T) {
	a := New(Config{Provider: &stubProvider{}, Model: "x", MaxTokens: 100})
	if _, err := a.RunVariants(context.Background(), "hi", nil); err == nil {
		t.Fatal("expected error for empty models")
	}
}
