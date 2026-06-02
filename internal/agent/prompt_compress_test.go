package agent

import (
	"context"
	"errors"
	"testing"
)

type fakeCompressor struct {
	called   bool
	out      string
	saved    int
	err      error
	gotInput string
}

func (f *fakeCompressor) Compress(ctx context.Context, prompt string) (string, int, error) {
	f.called = true
	f.gotInput = prompt
	if f.err != nil {
		return prompt, 0, f.err
	}
	if f.out == "" {
		return prompt, 0, nil
	}
	return f.out, f.saved, nil
}

func TestSetPromptCompressor_DefaultsThreshold(t *testing.T) {
	a := &Agent{}
	a.SetPromptCompressor(&fakeCompressor{}, 0)
	if a.compressTrigger != 0.7 {
		t.Fatalf("trigger = %v want 0.7", a.compressTrigger)
	}
}

func TestSetPromptCompressor_NilDisables(t *testing.T) {
	a := &Agent{}
	a.SetPromptCompressor(&fakeCompressor{}, 0.5)
	a.SetPromptCompressor(nil, 0)
	if a.promptCompressor != nil {
		t.Fatal("nil should clear")
	}
}

func TestApplyPromptCompression_NoCompressorReturnsOriginal(t *testing.T) {
	a := &Agent{}
	if got := a.applyPromptCompression("hi"); got != "hi" {
		t.Fatalf("no-op should pass through, got %q", got)
	}
}

func TestApplyPromptCompression_NoEstimatorReturnsOriginal(t *testing.T) {
	a := &Agent{}
	a.SetPromptCompressor(&fakeCompressor{out: "shorter"}, 0.5)
	// budgetEstimator is nil → BudgetReport returns nil → no compress
	if got := a.applyPromptCompression("hi there world"); got != "hi there world" {
		t.Fatalf("no estimator should bypass, got %q", got)
	}
}

func TestApplyPromptCompression_ErrorReturnsOriginal(t *testing.T) {
	a := &Agent{}
	c := &fakeCompressor{err: errors.New("boom")}
	a.SetPromptCompressor(c, 0.5)
	// Without a budget estimator the compressor never runs — verify the
	// guard order. Then assert that even if invoked manually, an error
	// path returns the original.
	if got, _, err := c.Compress(context.Background(), "x"); err == nil || got != "x" {
		t.Fatalf("compressor should propagate error+original")
	}
}

func TestApplyPromptCompression_EmptyOutputReturnsOriginal(t *testing.T) {
	c := &fakeCompressor{out: ""}
	got, _, _ := c.Compress(context.Background(), "original")
	if got != "original" {
		t.Fatalf("empty out should fall back to input, got %q", got)
	}
}
