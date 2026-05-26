package agent

import (
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/diagnostic"
)

func TestEscalator_FirstSightingUsesInitialTier(t *testing.T) {
	cases := map[string]diagnostic.Tier{
		"compile error: undefined: foo":     diagnostic.TierCompile,
		"--- FAIL: TestThing":               diagnostic.TierUnitTest,
		"panic: nil pointer dereference":    diagnostic.TierUnitTest,
		"vet ./...: bad declaration":        diagnostic.TierLint,
		"dial tcp: connection refused":      diagnostic.TierCurl,
		"some weird thing happened":         diagnostic.TierCompile,
	}
	for msg, want := range cases {
		t.Run(msg, func(t *testing.T) {
			e := newDiagnosticEscalator()
			got := e.suggest(msg)
			if diagnostic.Tier(got.Tier) != want {
				t.Errorf("initial tier for %q = %d (%s), want %d (%s)",
					msg, got.Tier, got.Name, want, want.Name())
			}
		})
	}
}

func TestEscalator_RepeatClimbsLadder(t *testing.T) {
	e := newDiagnosticEscalator()
	msg := "compile error: undefined: foo"
	first := e.suggest(msg)
	second := e.suggest(msg)
	if second.Tier <= first.Tier {
		t.Errorf("repeat sighting should climb: first=%d second=%d", first.Tier, second.Tier)
	}
	if second.Class != first.Class {
		t.Errorf("class shouldn't change on repeat: first=%s second=%s", first.Class, second.Class)
	}
}

func TestEscalator_DifferentClassesIndependent(t *testing.T) {
	e := newDiagnosticEscalator()
	compile1 := e.suggest("compile error: x")
	test1 := e.suggest("--- FAIL: TestY")
	if compile1.Class == test1.Class {
		t.Fatal("expected different classes")
	}
	// Second compile error should be the FIRST climb on compile, not test.
	compile2 := e.suggest("compile error: z")
	if compile2.Tier <= compile1.Tier {
		t.Errorf("compile didn't climb on second compile: first=%d second=%d",
			compile1.Tier, compile2.Tier)
	}
}

func TestEscalator_Saturates(t *testing.T) {
	e := newDiagnosticEscalator()
	last := -1
	for i := 0; i < 20; i++ {
		s := e.suggest("compile error: q")
		if i > 0 && s.Tier < last {
			t.Fatalf("tier went backward at iter %d: %d -> %d", i, last, s.Tier)
		}
		last = s.Tier
	}
	final := e.suggest("compile error: q")
	if !final.Exhausted {
		t.Errorf("expected Exhausted=true at top of ladder, got %+v", final)
	}
	if diagnostic.Tier(final.Tier) != diagnostic.TierHITLBash {
		t.Errorf("expected to saturate at HITLBash, got %d", final.Tier)
	}
}
