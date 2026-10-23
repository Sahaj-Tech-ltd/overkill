package compaction

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction/seahorse"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

// HistoryStrategy is structurally identical to agent.HistoryCompactor; keeping
// this package independent of agent avoids a runtime/agent dependency cycle.
type HistoryStrategy interface {
	Compact(context.Context, []providers.Message, string, int) ([]providers.Message, string, error)
}
type HistoryFactory struct {
	cfg      config.CompactionConfig
	strategy string
	Store    *seahorse.PostgresStore
}

func NewHistoryFactory(ctx context.Context, cfg config.CompactionConfig, db *sql.DB) (*HistoryFactory, error) {
	strategy, err := cfg.ResolveStrategy(db != nil)
	if err != nil {
		return nil, err
	}
	factory := &HistoryFactory{cfg: cfg, strategy: strategy}
	if strategy == "seahorse" {
		factory.Store, err = seahorse.NewPostgresStore(ctx, db)
		if err != nil {
			return nil, fmt.Errorf("compaction: initialize durable DAG: %w", err)
		}
	}
	return factory, nil
}
func (f *HistoryFactory) Strategy() string {
	if f == nil {
		return "legacy"
	}
	return f.strategy
}
func (f *HistoryFactory) Build(provider providers.Provider, sessionID string) (HistoryStrategy, bool, error) {
	if f == nil || f.strategy == "legacy" {
		return nil, false, nil
	}
	if provider == nil {
		return nil, false, fmt.Errorf("compaction: selected strategy requires provider")
	}
	preserve := f.cfg.PreserveMessages
	if preserve <= 0 {
		preserve = 20
	}
	if f.strategy == "lcm" {
		c := NewAgentCompactor(provider, tokenizer.NewEstimator(), preserve)
		c.SetCompactionModel(f.cfg.Model)
		return c, true, nil
	}
	model := f.cfg.Model
	if model == "" {
		models := provider.Models()
		if len(models) > 0 {
			best := models[0]
			for _, m := range models[1:] {
				if m.CostOut < best.CostOut {
					best = m
				}
			}
			model = best.ID
		}
	}
	opts := seahorse.DefaultHistoryOptions()
	opts.FreshTailCount = preserve
	opts.SummaryModel = model
	if f.cfg.MaxSummaryTokens > 0 {
		opts.LeafTargetTokens = f.cfg.MaxSummaryTokens
		opts.CondensedTargetTokens = f.cfg.MaxSummaryTokens
	}
	c, err := seahorse.NewHistoryCompactor(provider, tokenizer.NewEstimator(), f.Store, sessionID, opts)
	if err != nil {
		return nil, false, err
	}
	return c, true, nil
}
