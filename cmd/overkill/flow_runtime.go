package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// Compatibility socket writes are validated against the same persisted session
// identity as desktop turns. Alarm payloads never choose a cheap/global agent.
type canonicalDaemonFlowStore struct {
	services *runtimeServices
	sessions session.Store
	legacy   agent.FlowStore
}

func (s *canonicalDaemonFlowStore) Save(state *agent.FlowState) error {
	if state == nil {
		return fmt.Errorf("flow checkpoint: state required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release, acquired, err := s.services.FlowQueue.TrySessionOperation(ctx, state.SessionID)
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("flow checkpoint: owning session is busy")
	}
	defer release()
	metadata, err := s.sessions.Load(ctx, state.SessionID)
	if err != nil {
		return err
	}
	metadata, err = api.CanonicalSession(metadata)
	if err != nil {
		return err
	}
	model, provider := metadata.Model, metadata.Provider
	if model == "" {
		model = s.services.cfg.Agent.DefaultModel
	}
	if provider == "" {
		provider = s.services.cfg.Agent.DefaultProvider
	}
	if (state.Workspace != "" && state.Workspace != metadata.Folder) || (state.Model != "" && state.Model != model) || (state.Provider != "" && state.Provider != provider) {
		return fmt.Errorf("flow checkpoint: canonical identity mismatch")
	}
	bound := s.services.FlowQueue.Bind(metadata.ID, metadata.Folder, model, provider)
	if err := bound.BeginTurn(ctx); err != nil {
		return err
	}
	bound.ContinueTurn(state.CancelEpoch)
	return bound.Save(state)
}
func (s *canonicalDaemonFlowStore) Load(id string) (*agent.FlowState, error) {
	return s.legacy.Load(id)
}
func (s *canonicalDaemonFlowStore) Delete(id string) error            { return s.legacy.Delete(id) }
func (s *canonicalDaemonFlowStore) List() ([]*agent.FlowState, error) { return s.legacy.List() }
