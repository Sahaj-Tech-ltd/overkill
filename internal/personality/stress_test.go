package personality

import (
	"sync"
	"testing"
)

// ==========================================================================
// Adversarial stress tests: personality/memo edge cases.
// ==========================================================================

// MEMO-STRESS-1: pickMemo with single item (known panic trigger on large uint64)
// The bug: int(binary.LittleEndian.Uint64(b[:]))%len(items) can produce
// negative when uint64 > max int64, causing index out of range panic.
func TestStress_PickMemoSingleItem(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: pickMemo single item: %v", r)
		}
	}()
	// This must never panic
	result := pickMemo([]string{"only-one-item"})
	if result != "only-one-item" {
		t.Errorf("pickMemo single item returned %q", result)
	}
}

// MEMO-STRESS-2: pickMemo with many items to exercise random selection
func TestStress_PickMemoMultiItem(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: pickMemo multi item: %v", r)
		}
	}()
	items := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	seen := make(map[string]int)
	for i := 0; i < 1000; i++ {
		result := pickMemo(items)
		seen[result]++
	}
	// All items should appear at least occasionally
	for _, item := range items {
		if seen[item] == 0 {
			t.Logf("WARNING: item %q never selected in 1000 iterations (unlucky but not a bug)", item)
		}
	}
}

// MEMO-STRESS-3: Concurrent pickMemo calls
func TestStress_PickMemoConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	items := []string{"x", "y", "z"}
	panics := make(chan interface{}, 1000)

	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panics <- r
				}
			}()
			_ = pickMemo(items)
		}()
	}
	wg.Wait()
	close(panics)

	for p := range panics {
		t.Errorf("PANIC: concurrent pickMemo: %v", p)
	}
}

// MEMO-STRESS-4: MemoEngine Match with nil/empty defaults
func TestStress_MatchEmptyDefaults(t *testing.T) {
	e := &MemoEngine{
		defaults: []string{}, // empty defaults
		rules:    []compiledRule{},
		actions:  make(map[string][]string),
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Match with empty defaults: %v", r)
		}
	}()
	result := e.Match("any text")
	if result.Phrase == "" {
		t.Error("Match with empty defaults returned empty phrase")
	}
	t.Logf("Match with empty defaults: %q", result.Phrase)
}

// MEMO-STRESS-5: MemoEngine Match with nil engine
func TestStress_MatchNilEngine(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Match on nil engine: %v", r)
		}
	}()
	var e *MemoEngine
	_ = e.Match("hello")
}

// MEMO-STRESS-6: ActionMatch on nil engine
func TestStress_ActionMatchNilEngine(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: ActionMatch on nil engine: %v", r)
		}
	}()
	var e *MemoEngine
	_ = e.ActionMatch("web_search")
}

// MEMO-STRESS-7: AllPhrases on nil engine
func TestStress_AllPhrasesNilEngine(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: AllPhrases on nil engine: %v", r)
		}
	}()
	var e *MemoEngine
	result := e.AllPhrases()
	_ = result
}

// MEMO-STRESS-8: Learn with nil engine
func TestStress_LearnNilEngine(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Learn on nil engine: %v", r)
		}
	}()
	var e *MemoEngine
	_ = e.Learn(nil, []string{"a"}, []string{"b"}, "cat")
}

// MEMO-STRESS-9: Rules on nil engine
func TestStress_RulesNilEngine(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("PANIC: Rules on nil engine: %v", r)
		}
	}()
	var e *MemoEngine
	result := e.Rules()
	_ = result
}

// MEMO-STRESS-10: Concurrent Match + Learn on same engine (was already tested but reinforce)
func TestStress_MatchLearnRace(t *testing.T) {
	e := NewMemoEngine(nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			_ = e.Match("test pattern match")
		}(i)
		go func(n int) {
			defer wg.Done()
			_ = e.Learn(nil, []string{"newpattern"}, []string{"learned phrase"}, "test-cat")
		}(i)
	}
	wg.Wait()
}
