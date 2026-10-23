package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

func (s *Server) statusSession(ctx context.Context, id string) *RPCError {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return &RPCError{Code: InvalidParams, Message: "valid session_id is required"}
	}
	if loaded, err := s.sessionStore.Load(ctx, id); err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return &RPCError{Code: InvalidParams, Message: "session not found"}
		}
		return &RPCError{Code: InternalError, Message: "failed to load session"}
	} else if loaded == nil {
		return &RPCError{Code: InvalidParams, Message: "session not found"}
	}
	return nil
}
func (s *Server) runtimeForSession(ctx context.Context, params []byte) (agent.RuntimeStatusSnapshot, string, *RPCError) {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := unmarshalParams(params, &p); err != nil {
		return agent.RuntimeStatusSnapshot{}, "", err
	}
	if err := s.statusSession(ctx, p.SessionID); err != nil {
		return agent.RuntimeStatusSnapshot{}, "", err
	}
	s.mu.RLock()
	a := s.agents[p.SessionID]
	s.mu.RUnlock()
	if a != nil {
		return a.RuntimeStatusSnapshot(), "live", nil
	}
	restored, err := s.loadRuntimeStatus(ctx, p.SessionID)
	if err != nil {
		return agent.RuntimeStatusSnapshot{}, "", &RPCError{Code: InternalError, Message: "failed to load runtime status"}
	}
	if restored != nil {
		return *restored, "persisted", nil
	}
	_, durable := s.sessionStore.(session.RuntimeStatusStore)
	return agent.RuntimeStatusSnapshot{Version: 1, SessionID: p.SessionID, Verification: agent.VerificationStatus{Source: "post_write_verifier", Tests: []agent.VerificationCheck{}}, PersistenceAvailable: durable}, "unobserved", nil
}
func (s *Server) selfEvalStatus(ctx context.Context, params []byte) (interface{}, *RPCError) {
	snapshot, origin, rpcErr := s.runtimeForSession(ctx, params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return selfEvaluationStatus(snapshot, origin), nil
}

func selfEvaluationStatus(snapshot agent.RuntimeStatusSnapshot, origin string) map[string]any {
	v := snapshot.Verification
	state := "idle"
	if len(v.Tests) > 0 {
		state = v.Tests[len(v.Tests)-1].Status
	}
	phase := state
	var confidence *float64
	level := ""
	assessed := false
	if e := snapshot.Evaluation; e != nil {
		confidence = e.Score
		level = e.Level
		assessed = e.Status == "assessed"
		if len(v.Tests) == 0 {
			state = "confidence_" + e.Status
			phase = state
		}
		if e.Status == "running" {
			state = "running"
			phase = "assessing_confidence"
		}
	}
	active := v.Running || (snapshot.Evaluation != nil && snapshot.Evaluation.Status == "running")
	if v.Running {
		state = "running"
		phase = "verifying"
	}
	var iteration, maxIterations any
	if e := snapshot.Iterative; e != nil {
		iteration, maxIterations = e.Iteration, e.MaxIterations
		if e.Status == "running" || !active {
			state, phase = e.Status, "self_evaluation_"+e.Phase
		}
		active = active || e.Status == "running"
	}
	if p := snapshot.Plan; p != nil && p.Current {
		if p.Status == "running" {
			active = true
			state, phase = "running", "plan_execution"
		} else if !active {
			state, phase = p.Status, "plan_execution"
		}
	}
	return map[string]any{"session_id": snapshot.SessionID, "source": v.Source, "plan_execution": snapshot.Plan, "self_evaluation_available": snapshot.Iterative != nil, "iterative_evaluation": snapshot.Iterative, "confidence_assessment_available": assessed, "confidence": confidence, "confidence_level": level, "evaluation": snapshot.Evaluation, "iteration": iteration, "max_iterations": maxIterations, "red_team_passed": nil, "active": active, "phase": phase, "status": state, "verification": v, "snapshot_origin": origin, "persistence_available": snapshot.PersistenceAvailable, "persistence_failed": snapshot.PersistenceFailed, "message": "Actual post-write checks and confidence observations. Iterative availability indicates an observed loop invocation. Iterative evaluation runs only in explicitly configured auto mode with a wired loop. Acceptance means its confidence policy accepted; it is not test evidence. Revision calls emit status and final results, without token deltas. Red-team pass evidence is unavailable."}
}
func (s *Server) testResults(ctx context.Context, params []byte) (interface{}, *RPCError) {
	snapshot, origin, rpcErr := s.runtimeForSession(ctx, params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return struct {
		SessionID string `json:"session_id"`
		agent.VerificationStatus
		SnapshotOrigin       string `json:"snapshot_origin"`
		PersistenceAvailable bool   `json:"persistence_available"`
		PersistenceFailed    bool   `json:"persistence_failed"`
		Message              string `json:"message"`
	}{snapshot.SessionID, snapshot.Verification, origin, snapshot.PersistenceAvailable, snapshot.PersistenceFailed, "Actually invoked post-write checks, bounded to the latest 100 observations. Display excerpts are capped. Skipped, canceled and unconfirmed checks are never passes. Durable stores retain observations across restart; persisted in-flight checks without a live observer are unconfirmed."}, nil
}
func (s *Server) dashboardSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return "", false
	}
	id := r.URL.Query().Get("session_id")
	if err := s.statusSession(r.Context(), id); err != nil {
		code := http.StatusBadRequest
		if err.Code == InternalError {
			code = http.StatusInternalServerError
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Message})
		return "", false
	}
	return id, true
}
func (s *Server) dashboardGoal(w http.ResponseWriter, r *http.Request) {
	id, ok := s.dashboardSession(w, r)
	if !ok {
		return
	}
	if s.dashboardGoalReader == nil {
		http.Error(w, "goal status unavailable", http.StatusNotImplemented)
		return
	}
	g, err := s.dashboardGoalReader(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to read goal", http.StatusInternalServerError)
		return
	}
	if g == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": id, "goal": nil})
		return
	}
	status := g.Status
	if status == "" {
		status = "active"
		if !g.Active {
			status = "paused"
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"session_id": id, "objective": g.Text, "active": g.Active, "status": status, "token_budget": g.TokenBudget, "tokens_used": g.TokensUsed, "time_budget_s": g.TimeBudgetSeconds, "time_used_s": float64(g.TimeUsedNanoseconds) / 1e9})
}
func (s *Server) dashboardPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := s.dashboardSession(w, r)
	if !ok {
		return
	}
	if s.dashboardPlanReader == nil {
		http.Error(w, "plan status unavailable", http.StatusNotImplemented)
		return
	}
	p, err := s.dashboardPlanReader(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to read plan", http.StatusInternalServerError)
		return
	}
	if p == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": id, "plan": nil})
		return
	}
	if p.SessionID != id {
		http.Error(w, "plan session identity mismatch", http.StatusInternalServerError)
		return
	}
	items := make([]map[string]any, 0, len(p.Items))
	for _, item := range p.Items {
		status := "pending"
		if item.Done {
			status = "done"
		}
		items = append(items, map[string]any{"id": item.ID, "text": item.Text, "status": status})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"session_id": id, "title": p.Title, "items": items})
}
