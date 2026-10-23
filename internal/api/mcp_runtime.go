package api

import (
	"context"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/mcp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// startMCP is called under backgroundMu. Constructors perform no subprocess IO.
func (s *Server) startMCP(ctx context.Context) error {
	if s.mcpManager == nil {
		return nil
	}
	lifecycle, cancel := context.WithCancel(ctx)
	if err := s.mcpManager.Start(lifecycle); err != nil {
		cancel()
		return err
	}
	s.mcpCancel = cancel
	s.mcpDone = make(chan struct{})
	go func() {
		defer close(s.mcpDone)
		defer s.mcpManager.Stop()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			s.reconcileMCP()
			select {
			case <-lifecycle.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}
func (s *Server) reconcileMCP() {
	registries := []*tools.Registry{s.toolRegistry}
	s.mu.Lock()
	for _, runtime := range s.sessionRuntimes {
		if runtime != nil && runtime.Tools != nil {
			registries = append(registries, runtime.Tools)
		}
	}
	s.mu.Unlock()
	for _, registry := range registries {
		if registry == nil {
			continue
		}
		s.mcpManager.RescanTools(registry.Has, func(adapter *mcp.ToolAdapter) error { return registry.Register(adapter) })
	}
}
