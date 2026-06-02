package subagent

import (
	"math"
	"sync"
	"testing"
)

func TestCostRollup_AddChild(t *testing.T) {
	rollup := NewCostRollup("session-1")

	rollup.AddChild(&Result{
		TaskIndex: 0,
		TokensIn:  1500,
		TokensOut: 800,
		CostUSD:   0.003,
	})
	rollup.AddChild(&Result{
		TaskIndex: 1,
		TokensIn:  2000,
		TokensOut: 1200,
		CostUSD:   0.005,
	})

	s := rollup.Summary()
	if s.ChildrenCount != 2 {
		t.Errorf("ChildrenCount = %d, want 2", s.ChildrenCount)
	}
	if s.TotalIn != 3500 {
		t.Errorf("TotalIn = %d, want 3500", s.TotalIn)
	}
	if s.TotalOut != 2000 {
		t.Errorf("TotalOut = %d, want 2000", s.TotalOut)
	}
	if math.Abs(s.TotalCost-0.008) > 1e-9 {
		t.Errorf("TotalCost = %.10f, want 0.008", s.TotalCost)
	}
}

func TestCostRollup_Empty(t *testing.T) {
	rollup := NewCostRollup("session-empty")

	s := rollup.Summary()
	if s.ChildrenCount != 0 {
		t.Errorf("ChildrenCount = %d, want 0", s.ChildrenCount)
	}
	if s.TotalIn != 0 {
		t.Errorf("TotalIn = %d, want 0", s.TotalIn)
	}
	if s.TotalOut != 0 {
		t.Errorf("TotalOut = %d, want 0", s.TotalOut)
	}
	if s.TotalCost != 0 {
		t.Errorf("TotalCost = %f, want 0", s.TotalCost)
	}
}

func TestCostRollup_ConcurrentAdd(t *testing.T) {
	rollup := NewCostRollup("session-concurrent")

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rollup.AddChild(&Result{
					TokensIn:  100,
					TokensOut: 50,
					CostUSD:   0.001,
				})
			}
		}()
	}
	wg.Wait()

	s := rollup.Summary()
	if s.ChildrenCount != 100 {
		t.Errorf("ChildrenCount = %d, want 100", s.ChildrenCount)
	}
	if s.TotalIn != 10000 {
		t.Errorf("TotalIn = %d, want 10000", s.TotalIn)
	}
}

func TestCostRollup_SingleChild(t *testing.T) {
	rollup := NewCostRollup("session-single")

	rollup.AddChild(&Result{
		TaskIndex: 0,
		TokensIn:  500,
		TokensOut: 300,
		CostUSD:   0.002,
	})

	s := rollup.Summary()
	if s.ChildrenCount != 1 {
		t.Errorf("ChildrenCount = %d, want 1", s.ChildrenCount)
	}
	if s.TotalIn != 500 {
		t.Errorf("TotalIn = %d, want 500", s.TotalIn)
	}
	if s.TotalOut != 300 {
		t.Errorf("TotalOut = %d, want 300", s.TotalOut)
	}
	if s.TotalCost != 0.002 {
		t.Errorf("TotalCost = %f, want 0.002", s.TotalCost)
	}
}

func TestCostRollup_NilResult(t *testing.T) {
	rollup := NewCostRollup("session-nil")

	rollup.AddChild(nil)

	s := rollup.Summary()
	if s.ChildrenCount != 0 {
		t.Errorf("ChildrenCount = %d, want 0", s.ChildrenCount)
	}
	if s.TotalIn != 0 {
		t.Errorf("TotalIn = %d, want 0", s.TotalIn)
	}
	if s.TotalOut != 0 {
		t.Errorf("TotalOut = %d, want 0", s.TotalOut)
	}
	if s.TotalCost != 0 {
		t.Errorf("TotalCost = %f, want 0", s.TotalCost)
	}
}
