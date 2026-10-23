package api

import (
	"context"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

func (s *Server) sessionHistoryCompactor(ctx context.Context, sessionID string, provider providers.Provider, runtime *SessionRuntime, registry *tools.Registry) (agent.HistoryCompactor, bool, *tools.Registry, error) {
	if runtime != nil && runtime.HistoryCompactorFactory != nil {
		c, use, err := runtime.HistoryCompactorFactory(provider)
		return c, use, registry, err
	}
	factory, err := compaction.NewHistoryFactory(ctx, s.cfg.Compaction, s.db)
	if err != nil {
		return nil, false, registry, err
	}
	c, use, err := factory.Build(provider, sessionID)
	if err != nil {
		return nil, false, registry, err
	}
	if factory.Store == nil {
		return c, use, registry, nil
	}
	// A standalone API server may start with a shared registry. Clone it before
	// binding memory tools, otherwise one chat's identity could leak into another.
	bound := tools.NewRegistry()
	for _, name := range registry.List() {
		tool, err := registry.Get(name)
		if err != nil {
			return nil, false, registry, err
		}
		if err = bound.Register(tool); err != nil {
			return nil, false, registry, err
		}
	}
	for _, tool := range tools.NewDAGMemoryTools(factory.Store, sessionID) {
		if err = bound.Register(tool); err != nil {
			return nil, false, registry, err
		}
	}
	return c, use, bound, nil
}
