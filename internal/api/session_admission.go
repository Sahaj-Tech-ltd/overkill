package api

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// beginSessionOperation serializes a turn AND its durable commit with other
// turns and session mutations. Agent.IsRunning alone ends before persistence.
func (s *Server) beginSessionOperation(id string) (func(), error) {
	if id == "" {
		return nil, fmt.Errorf("session ID required")
	}
	s.admissionMu.Lock()
	if s.sessionOperations == nil {
		s.sessionOperations = make(map[string]bool)
	}
	if s.sessionOperations[id] {
		s.admissionMu.Unlock()
		return nil, fmt.Errorf("session %q is busy; retry after its current operation finishes", id)
	}
	s.sessionOperations[id] = true
	s.admissionMu.Unlock()
	var releaseDatabase func()
	if s.flowQueue != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		release, acquired, err := s.flowQueue.TrySessionOperation(ctx, id)
		cancel()
		if err != nil || !acquired {
			s.admissionMu.Lock()
			delete(s.sessionOperations, id)
			s.admissionMu.Unlock()
			if err != nil {
				return nil, fmt.Errorf("session admission unavailable")
			}
			return nil, fmt.Errorf("session %q is busy in another application process", id)
		}
		releaseDatabase = release
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if releaseDatabase != nil {
				releaseDatabase()
			}
			s.admissionMu.Lock()
			delete(s.sessionOperations, id)
			s.admissionMu.Unlock()
		})
	}, nil
}

// A cancellation belongs to one admitted operation. The generation pointer
// prevents delayed cleanup or abort from cancelling a later turn of the session.
type sessionCancellation struct{ cancel context.CancelFunc }

func (s *Server) bindSessionCancellation(parent context.Context, id string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	entry := &sessionCancellation{cancel: cancel}
	s.admissionMu.Lock()
	if s.sessionCancels == nil {
		s.sessionCancels = make(map[string]*sessionCancellation)
	}
	s.sessionCancels[id] = entry
	s.admissionMu.Unlock()
	return ctx, func() {
		cancel()
		s.admissionMu.Lock()
		if s.sessionCancels[id] == entry {
			delete(s.sessionCancels, id)
		}
		s.admissionMu.Unlock()
	}
}
