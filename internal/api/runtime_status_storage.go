package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"time"
)

func (s *Server) loadRuntimeStatus(ctx context.Context, id string) (*agent.RuntimeStatusSnapshot, error) {
	store, ok := s.sessionStore.(session.RuntimeStatusStore)
	if !ok {
		return nil, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := store.LoadRuntimeStatus(bounded, id)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	if len(data) > session.MaxRuntimeStatusBytes {
		return nil, fmt.Errorf("api: oversized runtime status")
	}
	var snapshot agent.RuntimeStatusSnapshot
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if err = agent.ValidateRuntimeStatus(snapshot, id); err != nil {
		return nil, err
	}
	snapshot = agent.RecoverRuntimeStatus(snapshot)
	snapshot.PersistenceAvailable = true
	snapshot.PersistenceFailed = false
	return &snapshot, nil
}
func (s *Server) attachRuntimeStatus(ctx context.Context, id string, a *agent.Agent) error {
	store, ok := s.sessionStore.(session.RuntimeStatusStore)
	if !ok {
		return nil
	}
	restored, err := s.loadRuntimeStatus(ctx, id)
	if err != nil {
		return err
	}
	return a.SetRuntimeStatusPersistence(restored, func(ctx context.Context, snapshot agent.RuntimeStatusSnapshot) error {
		data, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		return store.SaveRuntimeStatus(ctx, id, data)
	})
}
