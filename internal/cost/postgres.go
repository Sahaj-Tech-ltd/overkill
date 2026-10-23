package cost

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
	if t.db == nil {
		return fmt.Errorf("database is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:cost', 0))`); err != nil {
		return err
	}
	for _, statement := range []struct{ name, sql string }{
		{"cost_records table", `CREATE TABLE IF NOT EXISTS cost_records (
   id TEXT PRIMARY KEY, session_id TEXT NOT NULL DEFAULT '',
   model TEXT NOT NULL DEFAULT '', provider TEXT NOT NULL DEFAULT '',
   input_tokens BIGINT NOT NULL DEFAULT 0, output_tokens BIGINT NOT NULL DEFAULT 0,
   cached_tokens BIGINT NOT NULL DEFAULT 0, cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
   task_id TEXT NOT NULL DEFAULT '', timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
   created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`},
		{"input token width", `ALTER TABLE cost_records ALTER COLUMN input_tokens TYPE BIGINT`},
		{"output token width", `ALTER TABLE cost_records ALTER COLUMN output_tokens TYPE BIGINT`},
		{"cached token width", `ALTER TABLE cost_records ALTER COLUMN cached_tokens TYPE BIGINT`},
		{"session index", `CREATE INDEX IF NOT EXISTS idx_cost_records_session ON cost_records (session_id)`},
		{"timestamp index", `CREATE INDEX IF NOT EXISTS idx_cost_records_timestamp ON cost_records (timestamp)`},
	} {
		if _, err := tx.ExecContext(ctx, statement.sql); err != nil {
			return fmt.Errorf("%s: %w", statement.name, err)
		}
	}
	return tx.Commit()
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
	start, err := time.Parse("2006-01-02", date)
	if err != nil {
		return CostSummary{}, fmt.Errorf("cost: invalid UTC date: %w", err)
	}
	rows, err := t.db.QueryContext(ctx, `
		SELECT input_tokens, output_tokens, cached_tokens, cost_usd
		FROM cost_records WHERE timestamp >= $1 AND timestamp < $2
	`, start, start.AddDate(0, 0, 1))
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

	// Keep totals independent from the detail limit, using one snapshot so a
	// concurrent Record cannot make the totals disagree with the detail rows.
	tx, err := t.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("cost: usage transaction: %w", err)
	}
	defer tx.Rollback()
	report := &UsageReport{
		Entries:    make([]Entry, 0),
		ByModel:    make(map[string]CostSummary),
		ByProvider: make(map[string]CostSummary),
	}
	where := query[strings.Index(query, " WHERE "):]
	groups, err := tx.QueryContext(ctx, `SELECT model, provider, SUM(input_tokens), SUM(output_tokens),
		SUM(cached_tokens), SUM(cost_usd), COUNT(*) FROM cost_records`+where+` GROUP BY model, provider`, args...)
	if err != nil {
		return nil, fmt.Errorf("cost: usage totals: %w", err)
	}
	for groups.Next() {
		var model, provider string
		var total CostSummary
		if err := groups.Scan(&model, &provider, &total.InputTokens, &total.OutputTokens,
			&total.CachedTokens, &total.TotalUSD, &total.RequestCount); err != nil {
			groups.Close()
			return nil, fmt.Errorf("cost: usage totals scan: %w", err)
		}
		mergeCostSummary := func(dst CostSummary) CostSummary {
			dst.InputTokens += total.InputTokens
			dst.OutputTokens += total.OutputTokens
			dst.CachedTokens += total.CachedTokens
			dst.TotalUSD += total.TotalUSD
			dst.RequestCount += total.RequestCount
			return dst
		}
		report.Summary = mergeCostSummary(report.Summary)
		report.ByModel[model] = mergeCostSummary(report.ByModel[model])
		report.ByProvider[provider] = mergeCostSummary(report.ByProvider[provider])
	}
	if err := groups.Err(); err != nil {
		groups.Close()
		return nil, err
	}
	if err := groups.Close(); err != nil {
		return nil, err
	}
	query += " ORDER BY timestamp DESC, id"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cost: usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.SessionID, &e.Model, &e.Provider,
			&e.InputTokens, &e.OutputTokens, &e.CachedTokens, &e.CostUSD,
			&e.TaskID, &e.Timestamp); err != nil {
			return nil, fmt.Errorf("cost: usage scan: %w", err)
		}
		report.Entries = append(report.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return report, nil
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
