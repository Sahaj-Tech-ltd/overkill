package cost

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const (
	entryPrefix      = "cost:"
	sessionIdxPrefix = "idx:cost:session:"
	dateIdxPrefix    = "idx:cost:date:"
	tsDigits         = 19
)

type BadgerTracker struct {
	db     *badger.DB
	cfg    config.CostConfig
	mu     sync.RWMutex
	models map[string]providers.Model
}

func NewBadgerTracker(dir string, cfg config.CostConfig) (*BadgerTracker, error) {
	opts := badger.DefaultOptions(dir)
	opts.Logger = nil
	db, err := badger.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("cost: %w", err)
	}
	return &BadgerTracker{
		db:     db,
		cfg:    cfg,
		models: make(map[string]providers.Model),
	}, nil
}

func (bt *BadgerTracker) RegisterModel(m providers.Model) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.models[m.ID] = m
}

func (bt *BadgerTracker) lookupModel(id string) (providers.Model, bool) {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	m, ok := bt.models[id]
	return m, ok
}

func fmtTs(t time.Time) string {
	return fmt.Sprintf("%019d", t.UnixNano())
}

func eKey(ts, id string) string {
	return entryPrefix + ts + ":" + id
}

func sIdxKey(sid, ts, id string) string {
	return sessionIdxPrefix + sid + ":" + ts + ":" + id
}

func dIdxKey(date, ts, id string) string {
	return dateIdxPrefix + date + ":" + ts + ":" + id
}

func (bt *BadgerTracker) Record(ctx context.Context, entry Entry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.CostUSD == 0 {
		if m, ok := bt.lookupModel(entry.Model); ok {
			entry.CostUSD = CalculateCost(providers.Usage{
				InputTokens:       entry.InputTokens,
				OutputTokens:      entry.OutputTokens,
				CachedInputTokens: entry.CachedTokens,
			}, m)
		}
	}

	ts := fmtTs(entry.Timestamp)
	date := entry.Timestamp.Format("2006-01-02")
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("cost: %w", err)
	}

	err = bt.db.Update(func(txn *badger.Txn) error {
		if err := txn.Set([]byte(eKey(ts, entry.ID)), data); err != nil {
			return err
		}
		if entry.SessionID != "" {
			if err := txn.Set([]byte(sIdxKey(entry.SessionID, ts, entry.ID)), nil); err != nil {
				return err
			}
		}
		if err := txn.Set([]byte(dIdxKey(date, ts, entry.ID)), nil); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("cost: %w", err)
	}

	log.Debug().Str("entry_id", entry.ID).Float64("cost_usd", entry.CostUSD).Msg("recorded cost entry")
	return nil
}

func (bt *BadgerTracker) readEntry(txn *badger.Txn, key string) (*Entry, error) {
	item, err := txn.Get([]byte(key))
	if err != nil {
		return nil, err
	}
	var entry Entry
	if err := item.Value(func(val []byte) error {
		return json.Unmarshal(val, &entry)
	}); err != nil {
		return nil, err
	}
	return &entry, nil
}

func parseIndexKey(key string) (tsNano int64, entryID string, ok bool) {
	parts := strings.Split(key, ":")
	if len(parts) < 3 {
		return 0, "", false
	}
	tsStr := parts[len(parts)-2]
	id := parts[len(parts)-1]
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return ts, id, true
}

func (bt *BadgerTracker) SessionCost(ctx context.Context, sessionID string) (CostSummary, error) {
	var summary CostSummary
	prefix := sessionIdxPrefix + sessionID + ":"

	err := bt.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)
		opts.PrefetchValues = false
		iter := txn.NewIterator(opts)
		defer iter.Close()

		for iter.Rewind(); iter.Valid(); iter.Next() {
			ts, id, ok := parseIndexKey(string(iter.Item().Key()))
			if !ok {
				continue
			}
			tsStr := fmt.Sprintf("%019d", ts)
			entry, err := bt.readEntry(txn, eKey(tsStr, id))
			if err != nil {
				continue
			}
			addEntry(&summary, entry)
		}
		return nil
	})
	if err != nil {
		return CostSummary{}, fmt.Errorf("cost: %w", err)
	}
	return summary, nil
}

func (bt *BadgerTracker) DailyCost(ctx context.Context) (CostSummary, error) {
	today := time.Now().Format("2006-01-02")
	return bt.costForDate(today)
}

func (bt *BadgerTracker) costForDate(date string) (CostSummary, error) {
	var summary CostSummary
	prefix := dateIdxPrefix + date + ":"

	err := bt.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(prefix)
		opts.PrefetchValues = false
		iter := txn.NewIterator(opts)
		defer iter.Close()

		for iter.Rewind(); iter.Valid(); iter.Next() {
			ts, id, ok := parseIndexKey(string(iter.Item().Key()))
			if !ok {
				continue
			}
			tsStr := fmt.Sprintf("%019d", ts)
			entry, err := bt.readEntry(txn, eKey(tsStr, id))
			if err != nil {
				continue
			}
			addEntry(&summary, entry)
		}
		return nil
	})
	if err != nil {
		return CostSummary{}, fmt.Errorf("cost: %w", err)
	}
	return summary, nil
}

func (bt *BadgerTracker) RollingCost(ctx context.Context, window time.Duration) (CostSummary, error) {
	now := time.Now()
	return bt.rangeSum(now.Add(-window), now)
}

func (bt *BadgerTracker) rangeSum(start, end time.Time) (CostSummary, error) {
	var summary CostSummary
	seekKey := entryPrefix + fmtTs(start)

	err := bt.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte(entryPrefix)
		iter := txn.NewIterator(opts)
		defer iter.Close()

		for iter.Seek([]byte(seekKey)); iter.Valid(); iter.Next() {
			var entry Entry
			if err := iter.Item().Value(func(val []byte) error {
				return json.Unmarshal(val, &entry)
			}); err != nil {
				continue
			}
			if entry.Timestamp.After(end) {
				break
			}
			addEntry(&summary, &entry)
		}
		return nil
	})
	if err != nil {
		return CostSummary{}, fmt.Errorf("cost: %w", err)
	}
	return summary, nil
}

func (bt *BadgerTracker) CheckBudget(ctx context.Context, sessionID string) (BudgetStatus, error) {
	daily, err := bt.DailyCost(ctx)
	if err != nil {
		return BudgetStatus{}, fmt.Errorf("cost: %w", err)
	}

	session, err := bt.SessionCost(ctx, sessionID)
	if err != nil {
		return BudgetStatus{}, fmt.Errorf("cost: %w", err)
	}

	windowHrs := bt.cfg.RollingWindowHrs
	if windowHrs == 0 {
		windowHrs = 5
	}
	window := time.Duration(windowHrs) * time.Hour

	rolling, err := bt.RollingCost(ctx, window)
	if err != nil {
		return BudgetStatus{}, fmt.Errorf("cost: %w", err)
	}

	warnPct := bt.cfg.WarnAtPercent
	if warnPct == 0 {
		warnPct = 80
	}

	status := BudgetStatus{
		DailyUsed:   daily.TotalUSD,
		DailyLimit:  bt.cfg.DailyLimitUSD,
		TaskUsed:    session.TotalUSD,
		TaskLimit:   bt.cfg.PerTaskLimitUSD,
		RollingUsed: rolling.TotalUSD,
		Window:      window,
	}

	if bt.cfg.DailyLimitUSD > 0 {
		status.DailyPercent = (daily.TotalUSD / bt.cfg.DailyLimitUSD) * 100
		if daily.TotalUSD >= bt.cfg.DailyLimitUSD {
			status.ShouldAbort = true
		} else if status.DailyPercent >= float64(warnPct) {
			status.ShouldWarn = true
		}
	}

	if bt.cfg.PerTaskLimitUSD > 0 {
		status.TaskPercent = (session.TotalUSD / bt.cfg.PerTaskLimitUSD) * 100
		if session.TotalUSD >= bt.cfg.PerTaskLimitUSD {
			status.ShouldAbort = true
		} else if status.TaskPercent >= float64(warnPct) && !status.ShouldWarn {
			status.ShouldWarn = true
		}
	}

	return status, nil
}

func (bt *BadgerTracker) Usage(ctx context.Context, opts UsageOptions) (*UsageReport, error) {
	var start, end time.Time
	if !opts.StartTime.IsZero() {
		start = opts.StartTime
	}
	if !opts.EndTime.IsZero() {
		end = opts.EndTime
	}

	var allEntries []Entry
	seekKey := entryPrefix
	if !start.IsZero() {
		seekKey = entryPrefix + fmtTs(start)
	}

	err := bt.db.View(func(txn *badger.Txn) error {
		iterOpts := badger.DefaultIteratorOptions
		iterOpts.Prefix = []byte(entryPrefix)
		iter := txn.NewIterator(iterOpts)
		defer iter.Close()

		for iter.Seek([]byte(seekKey)); iter.Valid(); iter.Next() {
			var entry Entry
			if err := iter.Item().Value(func(val []byte) error {
				return json.Unmarshal(val, &entry)
			}); err != nil {
				continue
			}
			if !end.IsZero() && entry.Timestamp.After(end) {
				break
			}
			if opts.SessionID != "" && entry.SessionID != opts.SessionID {
				continue
			}
			if opts.Provider != "" && entry.Provider != opts.Provider {
				continue
			}
			if opts.Model != "" && entry.Model != opts.Model {
				continue
			}
			allEntries = append(allEntries, entry)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cost: %w", err)
	}

	report := &UsageReport{
		ByModel:    make(map[string]CostSummary),
		ByProvider: make(map[string]CostSummary),
	}

	for _, e := range allEntries {
		addEntry(&report.Summary, &e)

		ms := report.ByModel[e.Model]
		addEntry(&ms, &e)
		report.ByModel[e.Model] = ms

		ps := report.ByProvider[e.Provider]
		addEntry(&ps, &e)
		report.ByProvider[e.Provider] = ps
	}

	if opts.Limit > 0 && len(allEntries) > opts.Limit {
		report.Entries = allEntries[:opts.Limit]
	} else {
		report.Entries = allEntries
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

func (bt *BadgerTracker) Close() error {
	return bt.db.Close()
}
