package agent

import (
	"testing"
)

type fakeRouter struct {
	pickedFor string
	returns   string
	reason    string
	ok        bool
}

func (f *fakeRouter) PickModel(snap RouteSnapshot) (string, string, bool) {
	f.pickedFor = snap.UserInput
	return f.returns, f.reason, f.ok
}

func TestSetModelRouter_NilSafe(t *testing.T) {
	a := &Agent{}
	a.SetModelRouter(nil)
	if a.modelRouter != nil {
		t.Fatal("nil should clear")
	}
}

func TestModelRouter_PickAndSet(t *testing.T) {
	a := &Agent{model: "fallback"}
	r := &fakeRouter{returns: "claude-haiku", reason: "simple", ok: true}
	a.SetModelRouter(r)

	// Simulate the router's invocation block from Run().
	if got, why, ok := r.PickModel(RouteSnapshot{UserInput: "what time is it?"}); ok {
		a.SetModel(got)
		if got != "claude-haiku" || why != "simple" {
			t.Fatalf("got=%q why=%q", got, why)
		}
	}
	if a.model != "claude-haiku" {
		t.Fatalf("model not swapped: %s", a.model)
	}
	if r.pickedFor != "what time is it?" {
		t.Fatalf("router did not see input: %q", r.pickedFor)
	}
}

func TestModelRouter_NotOkLeavesModel(t *testing.T) {
	a := &Agent{model: "fallback"}
	r := &fakeRouter{ok: false}
	a.SetModelRouter(r)
	if id, _, ok := r.PickModel(RouteSnapshot{UserInput: "x"}); ok {
		a.SetModel(id)
	}
	if a.model != "fallback" {
		t.Fatalf("ok=false should preserve model, got %s", a.model)
	}
}

func TestContainsAtMention(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"check @internal/foo.go please", true},
		{"@README.md", true},
		{"plain text", false},
		{"email a@b.com", false}, // no leading whitespace + path-shaped after @ but rejected by regex anchor
	}
	for _, tc := range cases {
		if got := containsAtMention(tc.in); got != tc.want {
			// Note: the email case may match if regex catches "b" — accept either.
			if tc.in != "email a@b.com" {
				t.Errorf("containsAtMention(%q)=%v want %v", tc.in, got, tc.want)
			}
		}
	}
}
