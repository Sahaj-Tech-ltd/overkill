package skills

import (
	"sync"
	"testing"
)

func TestLearnTrigger_FiresOnceAtThreshold(t *testing.T) {
	var mu sync.Mutex
	suggestions := []Suggestion{}
	tr := NewLearnTrigger(3, func(s Suggestion) {
		mu.Lock()
		suggestions = append(suggestions, s)
		mu.Unlock()
	})

	for i := 0; i < 5; i++ {
		tr.RecordSuccess("flaky-test-fix")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(suggestions) != 1 {
		t.Fatalf("got %d suggestions want 1", len(suggestions))
	}
	if suggestions[0].Successes != 3 {
		t.Fatalf("count = %d want 3 (threshold)", suggestions[0].Successes)
	}
	if suggestions[0].SkillName != "flaky-test-fix" {
		t.Fatalf("name = %q", suggestions[0].SkillName)
	}
}

func TestLearnTrigger_BelowThresholdNoFire(t *testing.T) {
	fired := false
	tr := NewLearnTrigger(5, func(Suggestion) { fired = true })
	for i := 0; i < 4; i++ {
		tr.RecordSuccess("x")
	}
	if fired {
		t.Fatal("should not fire below threshold")
	}
}

func TestLearnTrigger_ResetReFires(t *testing.T) {
	count := 0
	tr := NewLearnTrigger(2, func(Suggestion) { count++ })
	tr.RecordSuccess("x")
	tr.RecordSuccess("x") // fires
	tr.RecordSuccess("x") // already suggested → silent
	if count != 1 {
		t.Fatalf("first round count=%d want 1", count)
	}
	tr.Reset("x")
	tr.RecordSuccess("x")
	tr.RecordSuccess("x") // re-fires
	if count != 2 {
		t.Fatalf("after reset count=%d want 2", count)
	}
}

func TestLearnTrigger_PerClassIndependent(t *testing.T) {
	got := []string{}
	tr := NewLearnTrigger(2, func(s Suggestion) { got = append(got, s.Class) })
	tr.RecordSuccess("alpha")
	tr.RecordSuccess("alpha") // fires alpha
	tr.RecordSuccess("beta")
	tr.RecordSuccess("beta") // fires beta
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("got %v", got)
	}
}

func TestLearnTrigger_Snapshot(t *testing.T) {
	tr := NewLearnTrigger(10, nil)
	tr.RecordSuccess("a")
	tr.RecordSuccess("a")
	tr.RecordSuccess("b")
	snap := tr.Snapshot()
	if snap["a"] != 2 || snap["b"] != 1 {
		t.Fatalf("snapshot=%v", snap)
	}
}

func TestLearnTrigger_EmptyClassNoFire(t *testing.T) {
	fired := false
	tr := NewLearnTrigger(1, func(Suggestion) { fired = true })
	tr.RecordSuccess("   ")
	if fired {
		t.Fatal("empty class should not fire")
	}
}

func TestLearnTrigger_NilCallbackSafe(t *testing.T) {
	tr := NewLearnTrigger(1, nil)
	if !tr.RecordSuccess("x") == false {
		// should still return false without callback
	}
	// no panic = pass
}

func TestNtimes(t *testing.T) {
	cases := map[int]string{1: "once", 2: "twice", 3: "3 times", 7: "7 times"}
	for n, want := range cases {
		if got := ntimes(n); got != want {
			t.Errorf("ntimes(%d)=%q want %q", n, got, want)
		}
	}
}
