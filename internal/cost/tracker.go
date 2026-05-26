package cost

import (
	"context"
	"time"
)

type Tracker interface {
	Record(ctx context.Context, entry Entry) error
	SessionCost(ctx context.Context, sessionID string) (CostSummary, error)
	DailyCost(ctx context.Context) (CostSummary, error)
	RollingCost(ctx context.Context, window time.Duration) (CostSummary, error)
	CheckBudget(ctx context.Context, sessionID string) (BudgetStatus, error)
	Usage(ctx context.Context, opts UsageOptions) (*UsageReport, error)
	Close() error
}

type Entry struct {
	ID           string
	SessionID    string
	Model        string
	Provider     string
	Timestamp    time.Time
	InputTokens  int
	OutputTokens int
	CachedTokens int
	CostUSD      float64
	TaskID       string
}

type CostSummary struct {
	TotalUSD     float64
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	RequestCount int64
}

type BudgetStatus struct {
	DailyUsed    float64
	DailyLimit   float64
	DailyPercent float64
	TaskUsed     float64
	TaskLimit    float64
	TaskPercent  float64
	RollingUsed  float64
	Window       time.Duration
	ShouldWarn   bool
	ShouldAbort  bool
}

type UsageOptions struct {
	SessionID string
	Provider  string
	Model     string
	StartTime time.Time
	EndTime   time.Time
	Limit     int
}

type UsageReport struct {
	Summary    CostSummary
	ByModel    map[string]CostSummary
	ByProvider map[string]CostSummary
	Entries    []Entry
}
