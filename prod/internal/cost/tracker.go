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
	ID           string    `json:"id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Model        string    `json:"model,omitempty"`
	Provider     string    `json:"provider,omitempty"`
	Timestamp    time.Time `json:"timestamp,omitempty"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CachedTokens int       `json:"cached_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	TaskID       string    `json:"task_id,omitempty"`
}

type CostSummary struct {
	TotalUSD     float64 `json:"total_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedTokens int64   `json:"cached_tokens"`
	RequestCount int64   `json:"request_count"`
}

type BudgetStatus struct {
	DailyUsed    float64       `json:"daily_used"`
	DailyLimit   float64       `json:"daily_limit"`
	DailyPercent float64       `json:"daily_percent"`
	TaskUsed     float64       `json:"task_used"`
	TaskLimit    float64       `json:"task_limit"`
	TaskPercent  float64       `json:"task_percent"`
	RollingUsed  float64       `json:"rolling_used"`
	Window       time.Duration `json:"window_seconds"`
	ShouldWarn   bool          `json:"should_warn"`
	ShouldAbort  bool          `json:"should_abort"`
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
	Summary    CostSummary            `json:"summary"`
	ByModel    map[string]CostSummary `json:"by_model"`
	ByProvider map[string]CostSummary `json:"by_provider"`
	Entries    []Entry                `json:"entries,omitempty"`
}
