package api

import (
	"context"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/google/uuid"
)

// refreshRuntimePresence publishes only runtimes actually owned by this API
// process. A registered but unloaded peer has no observation from this server.
func (s *Server) refreshRuntimePresence(ctx context.Context) error {
	s.backgroundMu.Lock()
	ownerCtx := s.presenceContext
	available := s.backgroundStarted && !s.backgroundClosed && ownerCtx != nil && ownerCtx.Err() == nil
	s.backgroundMu.Unlock()
	if !available || s.coordinationStore == nil {
		return nil
	}
	if !s.presenceMu.TryLock() {
		return nil
	}
	defer s.presenceMu.Unlock()
	// Recheck after waiting for a previous snapshot so shutdown cannot republish.
	if ownerCtx.Err() != nil {
		return nil
	}
	if s.presenceObserver == "" {
		s.presenceObserver = uuid.NewString()
	}
	s.mu.RLock()
	states := make(map[string]string, len(s.agents))
	for id, a := range s.agents {
		if a != nil {
			states[id] = "ready"
			if a.IsRunning() {
				states[id] = "working"
			}
		}
	}
	s.mu.RUnlock()
	s.admissionMu.Lock()
	for id := range states {
		if s.sessionOperations[id] && states[id] == "ready" {
			states[id] = "busy"
		}
	}
	s.admissionMu.Unlock()
	s.pendingQuestionMu.RLock()
	for id := range states {
		if s.pendingQuestionData[id] != nil {
			states[id] = "waiting_for_input"
		}
	}
	s.pendingQuestionMu.RUnlock()
	observations := make([]coordination.RuntimeObservation, 0, len(states))
	for id, state := range states {
		observations = append(observations, coordination.RuntimeObservation{SessionID: id, State: state})
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.coordinationStore.PublishRuntimePresence(writeCtx, s.presenceObserver, observations)
}

func (s *Server) runRuntimePresence(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	defer func() {
		s.presenceMu.Lock()
		defer s.presenceMu.Unlock()
		if s.presenceObserver != "" {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = s.coordinationStore.RemoveRuntimeObserver(cleanup, s.presenceObserver)
		}
	}()
	for {
		if err := s.refreshRuntimePresence(ctx); err != nil && ctx.Err() == nil {
			s.backgroundMu.Lock()
			s.backgroundError = true
			s.backgroundMu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
