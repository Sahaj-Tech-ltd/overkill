package subagent

import "sync"

// RollupSummary is a point-in-time snapshot of aggregated child agent costs.
type RollupSummary struct {
	ChildrenCount int
	TotalIn       int64
	TotalOut      int64
	TotalCost     float64
}

// CostRollup folds child agent token/cost spend into an aggregated total.
// All methods are safe for concurrent use.
type CostRollup struct {
	mu        sync.Mutex
	sessionID string
	count     int
	totalIn   int64
	totalOut  int64
	totalCost float64
}

// NewCostRollup creates a new CostRollup associated with the given session.
func NewCostRollup(sessionID string) *CostRollup {
	return &CostRollup{
		sessionID: sessionID,
	}
}

// AddChild folds one child's token usage and cost into the rollup.
// Passing nil is a safe no-op.
func (c *CostRollup) AddChild(result *Result) {
	if result == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.count++
	c.totalIn += result.TokensIn
	c.totalOut += result.TokensOut
	c.totalCost += result.CostUSD
}

// Summary returns a snapshot of the aggregated totals.
func (c *CostRollup) Summary() RollupSummary {
	c.mu.Lock()
	defer c.mu.Unlock()

	return RollupSummary{
		ChildrenCount: c.count,
		TotalIn:       c.totalIn,
		TotalOut:      c.totalOut,
		TotalCost:     c.totalCost,
	}
}
