package cost

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	_ "github.com/lib/pq"
)

type PostgresTracker struct {
	db     *sql.DB
	cfg    config.CostConfig
	mu     sync.RWMutex
	models map[string]providers.Model
}

func NewPostgresTracker(db *sql.DB, cfg config.CostConfig) (*PostgresTracker, error) {
	t := &PostgresTracker{
		db:     db,
		cfg:    cfg,
		models: make(map[string]providers.Model),
	}
	if err := t.migrate(); err != nil {
		return nil, fmt.Errorf("cost: migrate: %w", err)
	}
	return t, nil
}

func (t *PostgresTracker) migrate() error {
	_, err := t.db.Exec(`
		CREATE TABLE IF NOT EXISTS cost_records (
			id              TEXT PRIMARY KEY,
			session_id      TEXT NOT NULL DEFAULT '',
			model           TEXT NOT NULL DEFAULT '',
			provider        TEXT NOT NULL DEFAULT '',
			input_tokens    INTEGER NOT NULL DEFAULT 0,
			output_tokens   INTEGER NOT NULL DEFAULT 0,
			cached_tokens   INTEGER NOT NULL DEFAULT 0,
			cost_usd        REAL NOT NULL DEFAULT 0,
			task_id         TEXT NOT NULL DEFAULT '',
			timestamp       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	for _, idx := range []string{
		`CREATE INDEX IF NOT EXISTS idx_cost_records_session ON cost_records (session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cost_records_timestamp ON cost_records (timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_cost_records_date ON cost_records ((timestamp::date))`,
	} {
		if _, err := t.db.Exec(idx); err != nil {
			return err
		}
	}
	return nil
}

func (t *PostgresTracker) RegisterModel(m providers.Model) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.models[m.ID] = m
}

func (t *PostgresTracker) lookupModel(id string) (providers.Model, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m, ok := t.models[id]
	return m, ok
}

func (t *PostgresTracker) Record(ctx context.Context, entry Entry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.CostUSD == 0 {
		if m, ok := t.lookupModel(entry.Model); ok {
			entry.CostUSD = CalculateCost(providers.Usage{
				InputTokens:       entry.InputTokens,
				OutputTokens:      entry.OutputTokens,
				CachedInputTokens: entry.CachedTokens,
			}, m)
		}
	}

	_, err := t.db.ExecContext(ctx, `
		INSERT INTO cost_records (id, session_id, model, provider, input_tokens, output_tokens, cached_tokens, cost_usd, task_id, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			session_id    = EXCLUDED.session_id,
			model         = EXCLUDED.model,
			provider      = EXCLUDED.provider,
			input_tokens  = EXCLUDED.input_tokens,
			output_tokens = EXCLUDED.output_tokens,
			cached_tokens = EXCLUDED.cached_tokens,
			cost_usd      = EXCLUDED.cost_usd,
			task_id       = EXCLUDED.task_id,
			timestamp     = EXCLUDED.timestamp
	`, entry.ID, entry.SessionID, entry.Model, entry.Provider,
		entry.InputTokens, entry.OutputTokens, entry.CachedTokens, entry.CostUSD, entry.TaskID, entry.Timestamp)
	if err != nil {
		return fmt.Errorf("cost: record: %w", err)
	}

	log.Debug().Str("entry_id", entry.ID).Float64("cost_usd", entry.CostUSD).Msg("recorded cost entry")
	return nil
}

func (t *PostgresTracker) SessionCost(ctx context.Context, sessionID string) (CostSummary, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT input_tokens, output_tokens, cached_tokens, cost_usd
		FROM cost_records WHERE session_id = $1
	`, sessionID)
	if err != nil {
		return CostSummary{}, fmt.Errorf("cost: session cost: %w", err)
	}
	defer rows.Close()

	var sum CostSummary
	for rows.Next() {
		var in, out, cached int
		var usd float64
		if err := rows.Scan(&in, &out, &cached, &usd); err != nil {
			log.Warn().Err(err).Msg("cost: session cost scan error")
			continue
		}
		sum.InputTokens += int64(in)
		sum.OutputTokens += int64(out)
		sum.CachedTokens += int64(cached)
		sum.TotalUSD += usd
		sum.RequestCount++
	}
	return sum, rows.Err()
}

func (t *PostgresTracker) DailyCost(ctx context.Context) (CostSummary, error) {
	today := time.Now().UTC().Format("2006-01-02")
	return t.costForDate(ctx, today)
}

func (t *PostgresTracker) costForDate(ctx context.Context, date string) (CostSummary, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT input_tokens, output_tokens, cached_tokens, cost_usd
		FROM cost_records WHERE timestamp::date = $1::date
	`, date)
	if err != nil {
		return CostSummary{}, fmt.Errorf("cost: date cost: %w", err)
	}
	defer rows.Close()

	var sum CostSummary
	for rows.Next() {
		var in, out, cached int
		var usd float64
		if err := rows.Scan(&in, &out, &cached, &usd); err != nil {
			log.Warn().Err(err).Msg("cost: session cost scan error")
			continue
		}
		sum.InputTokens += int64(in)
		sum.OutputTokens += int64(out)
		sum.CachedTokens += int64(cached)
		sum.TotalUSD += usd
		sum.RequestCount++
	}
	return sum, rows.Err()
}

func (t *PostgresTracker) RollingCost(ctx context.Context, window time.Duration) (CostSummary, error) {
	now := time.Now().UTC()
	return t.rangeSum(ctx, now.Add(-window), now)
}

func (t *PostgresTracker) rangeSum(ctx context.Context, start, end time.Time) (CostSummary, error) {
	rows, err := t.db.QueryContext(ctx, `
		SELECT input_tokens, output_tokens, cached_tokens, cost_usd
		FROM cost_records WHERE timestamp >= $1 AND timestamp <= $2
	`, start, end)
	if err != nil {
		return CostSummary{}, fmt.Errorf("cost: range sum: %w", err)
	}
	defer rows.Close()

	var sum CostSummary
	for rows.Next() {
		var in, out, cached int
		var usd float64
		if err := rows.Scan(&in, &out, &cached, &usd); err != nil {
			log.Warn().Err(err).Msg("cost: session cost scan error")
			continue
		}
		sum.InputTokens += int64(in)
		sum.OutputTokens += int64(out)
		sum.CachedTokens += int64(cached)
		sum.TotalUSD += usd
		sum.RequestCount++
	}
	return sum, rows.Err()
}

func (t *PostgresTracker) CheckBudget(ctx context.Context, sessionID string) (BudgetStatus, error) {
	daily, err := t.DailyCost(ctx)
	if err != nil {
		return BudgetStatus{}, fmt.Errorf("cost: %w", err)
	}

	session, err := t.SessionCost(ctx, sessionID)
	if err != nil {
		return BudgetStatus{}, fmt.Errorf("cost: %w", err)
	}

	windowHrs := t.cfg.RollingWindowHrs
	if windowHrs == 0 {
		windowHrs = 5
	}
	window := time.Duration(windowHrs) * time.Hour

	rolling, err := t.RollingCost(ctx, window)
	if err != nil {
		return BudgetStatus{}, fmt.Errorf("cost: %w", err)
	}

	warnPct := t.cfg.WarnAtPercent
	if warnPct == 0 {
		warnPct = 80
	}

	status := BudgetStatus{
		DailyUsed:   daily.TotalUSD,
		DailyLimit:  t.cfg.DailyLimitUSD,
		TaskUsed:    session.TotalUSD,
		TaskLimit:   t.cfg.PerTaskLimitUSD,
		RollingUsed: rolling.TotalUSD,
		Window:      window,
	}

	if t.cfg.DailyLimitUSD > 0 {
		status.DailyPercent = (daily.TotalUSD / t.cfg.DailyLimitUSD) * 100
		if daily.TotalUSD >= t.cfg.DailyLimitUSD {
			status.ShouldAbort = true
		} else if status.DailyPercent >= float64(warnPct) {
			status.ShouldWarn = true
		}
	}

	if t.cfg.PerTaskLimitUSD > 0 {
		status.TaskPercent = (session.TotalUSD / t.cfg.PerTaskLimitUSD) * 100
		if session.TotalUSD >= t.cfg.PerTaskLimitUSD {
			status.ShouldAbort = true
		} else if status.TaskPercent >= float64(warnPct) && !status.ShouldWarn {
			status.ShouldWarn = true
		}
	}

	if t.cfg.RollingLimitUSD > 0 {
		pct := (rolling.TotalUSD / t.cfg.RollingLimitUSD) * 100
		if rolling.TotalUSD >= t.cfg.RollingLimitUSD {
			status.ShouldAbort = true
		} else if pct >= float64(warnPct) && !status.ShouldWarn {
			status.ShouldWarn = true
		}
	}

	return status, nil
}

func (t *PostgresTracker) Usage(ctx context.Context, opts UsageOptions) (*UsageReport, error) {
	query := `SELECT id, session_id, model, provider, input_tokens, output_tokens, cached_tokens, cost_usd, task_id, timestamp
		FROM cost_records WHERE 1=1`
	var args []any
	argIdx := 1

	if opts.SessionID != "" {
		query += fmt.Sprintf(" AND session_id = $%d", argIdx)
		args = append(args, opts.SessionID)
		argIdx++
	}
	if opts.Provider != "" {
		query += fmt.Sprintf(" AND provider = $%d", argIdx)
		args = append(args, opts.Provider)
		argIdx++
	}
	if opts.Model != "" {
		query += fmt.Sprintf(" AND model = $%d", argIdx)
		args = append(args, opts.Model)
		argIdx++
	}
	if !opts.StartTime.IsZero() {
		query += fmt.Sprintf(" AND timestamp >= $%d", argIdx)
		args = append(args, opts.StartTime)
		argIdx++
	}
	if !opts.EndTime.IsZero() {
		query += fmt.Sprintf(" AND timestamp <= $%d", argIdx)
		args = append(args, opts.EndTime)
		argIdx++
	}

	query += " ORDER BY timestamp DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
		argIdx++
	}

	rows, err := t.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cost: usage: %w", err)
	}
	defer rows.Close()

	report := &UsageReport{
		ByModel:    make(map[string]CostSummary),
		ByProvider: make(map[string]CostSummary),
	}

	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Model, &e.Provider,
			&e.InputTokens, &e.OutputTokens, &e.CachedTokens, &e.CostUSD,
			&e.TaskID, &e.Timestamp); err != nil {
			log.Warn().Err(err).Msg("cost: usage scan error")
			continue
		}
		report.Entries = append(report.Entries, e)
		addEntry(&report.Summary, &e)

		ms := report.ByModel[e.Model]
		addEntry(&ms, &e)
		report.ByModel[e.Model] = ms

		ps := report.ByProvider[e.Provider]
		addEntry(&ps, &e)
		report.ByProvider[e.Provider] = ps
	}

	return report, rows.Err()
}

func addEntry(s *CostSummary, e *Entry) {
	s.TotalUSD += e.CostUSD
	s.InputTokens += int64(e.InputTokens)
	s.OutputTokens += int64(e.OutputTokens)
	s.CachedTokens += int64(e.CachedTokens)
	s.RequestCount++
}

func (t *PostgresTracker) Close() error {
	return nil
}
