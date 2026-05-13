package agent

import "testing"

type passthroughFilter struct{}

func (passthroughFilter) Filter(s string) string { return s }

type uppercaseFilter struct{}

func (uppercaseFilter) Filter(s string) string {
	// Naive uppercase via byte iteration — enough for the test.
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
	return string(b)
}

type emptyingFilter struct{}

func (emptyingFilter) Filter(string) string { return "" }

type panickingFilter struct{}

func (panickingFilter) Filter(string) string { panic("intentional") }

func TestApplyResponseFilter_NoFilterIsPassthrough(t *testing.T) {
	a := &Agent{}
	if got := a.applyResponseFilter("hello world"); got != "hello world" {
		t.Errorf("no filter should pass content unchanged, got %q", got)
	}
}

func TestApplyResponseFilter_FilterApplied(t *testing.T) {
	a := &Agent{}
	a.SetResponseFilter(uppercaseFilter{})
	if got := a.applyResponseFilter("hello"); got != "HELLO" {
		t.Errorf("filter should run, got %q", got)
	}
}

func TestApplyResponseFilter_EmptyOutputFallsBack(t *testing.T) {
	a := &Agent{}
	a.SetResponseFilter(emptyingFilter{})
	// Non-empty input + empty filter result → keep original.
	if got := a.applyResponseFilter("important content"); got != "important content" {
		t.Errorf("filter returning empty on non-empty input should fall back, got %q", got)
	}
	// Empty input → empty result is fine.
	if got := a.applyResponseFilter(""); got != "" {
		t.Errorf("empty input should stay empty, got %q", got)
	}
}

func TestApplyResponseFilter_PanicRecovered(t *testing.T) {
	a := &Agent{}
	a.SetResponseFilter(panickingFilter{})
	// Must not propagate the panic.
	got := a.applyResponseFilter("safe?")
	// Defensive fallback: empty result on panic → keep original.
	if got != "safe?" {
		t.Errorf("panic should fall back to original, got %q", got)
	}
}

func TestApplyResponseFilter_NilClears(t *testing.T) {
	a := &Agent{}
	a.SetResponseFilter(passthroughFilter{})
	if a.applyResponseFilter("x") != "x" {
		t.Error("passthrough filter installed but didn't run")
	}
	a.SetResponseFilter(nil)
	if a.applyResponseFilter("y") != "y" {
		t.Error("nil filter should be a no-op (no panic, identity)")
	}
}
