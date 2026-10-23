package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
)

// startFlowResumes is called while backgroundMu is held. Lifecycle belongs to
// the same serve process that owns desktop turns and their canonical runtimes.
func (s *Server) startFlowResumes(ctx context.Context) {
	if s.flowQueue == nil {
		return
	}
	ctx, s.flowCancel = context.WithCancel(ctx)
	s.flowDone = make(chan struct{})
	go func() {
		defer close(s.flowDone)
		var workers sync.WaitGroup
		defer workers.Wait()
		slots := make(chan struct{}, 4)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			ids, err := s.flowQueue.Due(ctx)
			if err != nil && ctx.Err() == nil {
				log.Printf("flow resume: cannot inspect durable queue")
			}
			for _, id := range ids {
				select {
				case slots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				workers.Add(1)
				go func(id string) { defer workers.Done(); defer func() { <-slots }(); s.resumeQueuedFlow(ctx, id) }(id)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}
func (s *Server) resumeQueuedFlow(parent context.Context, id string) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	sessionID, err := s.flowQueue.Session(ctx, id)
	if err != nil {
		return
	}
	release, err := s.beginSessionOperation(sessionID)
	if err != nil {
		s.flowQueue.DeferBusy(ctx, id)
		return
	}
	defer release()
	ctx, stop := s.bindSessionCancellation(ctx, sessionID)
	defer stop()
	lease, err := s.flowQueue.Claim(ctx, id)
	if err != nil || lease == nil {
		return
	}
	defer lease.Close()
	finish := func(status, detail string) {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := lease.Finish(finishCtx, status, detail); err != nil {
			log.Printf("flow resume: final state not committed")
		}
	}
	state := lease.State
	metadata, err := s.sessionStore.Load(ctx, sessionID)
	if err != nil {
		finish("cancelled", "owning session unavailable")
		return
	}
	if metadata.Status != "active" {
		finish("cancelled", "owning session is inactive")
		return
	}
	canonical, err := CanonicalSession(metadata)
	if err != nil || canonical.Folder != state.Workspace {
		finish("cancelled", "owning workspace changed")
		return
	}
	// A new human turn supersedes the old pending continuation. History is
	// compared as data; the public RPC never accepts caller-supplied history.
	expected, _ := json.Marshal(state.History)
	actual, _ := json.Marshal(metadata.Messages)
	if !bytes.Equal(expected, actual) {
		finish("cancelled", "conversation changed after checkpoint")
		return
	}
	model, provider := metadata.Model, metadata.Provider
	s.mu.RLock()
	if model == "" {
		model = s.cfg.Agent.DefaultModel
	}
	if provider == "" {
		provider = s.cfg.Agent.DefaultProvider
	}
	s.mu.RUnlock()
	if model != state.Model || provider != state.Provider {
		finish("cancelled", "provider or model changed after checkpoint")
		return
	}
	a, rpcErr := s.getOrCreateAdmittedAgent(ctx, sessionID)
	if rpcErr != nil {
		finish("failed", "runtime unavailable")
		return
	}
	stream, err := a.ContinueStream(ctx, state)
	if err != nil {
		finish("manual_review", "continuation could not be admitted")
		return
	}
	complete, checkpointed, failed := false, false, false
	for event := range stream {
		if event.Type == agent.EventDone {
			complete = event.Result != nil && !event.Result.Blocked
			if _, ok := event.Metadata["flow_checkpoint"]; ok {
				checkpointed = true
			}
		}
		if event.Type == agent.EventError {
			failed = true
		}
	}
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	err = s.saveSessionState(persistCtx, sessionID, a)
	persistCancel()
	if err != nil {
		finish("manual_review", "continuation transcript could not be committed")
		return
	}
	if checkpointed {
		return
	} // the same flow's newer queued generation owns retry
	if failed || !complete {
		finish("manual_review", "continuation stopped; inspect completed tool effects before retry")
		return
	}
	finish("completed", "")
}

func (s *Server) handleFlowList(ctx context.Context, params []byte) (interface{}, *RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalParams(params, &p); err != nil {
		return nil, err
	}
	if err := s.statusSession(ctx, p.SessionID); err != nil {
		return nil, err
	}
	if s.flowQueue == nil {
		return map[string]any{"enabled": false, "flows": []agent.FlowJobStatus{}}, nil
	}
	flows, err := s.flowQueue.ListSession(ctx, p.SessionID)
	if err != nil {
		return nil, &RPCError{Code: InternalError, Message: "flow status unavailable"}
	}
	return map[string]any{"enabled": true, "flows": flows}, nil
}
