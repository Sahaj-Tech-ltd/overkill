package agent

import (
	"context"
	"fmt"
	"math"
	"time"
)

// ConfidenceObservation is a real heuristic computation, not a test pass or
// evidence that the optional iterative SelfEvaluateLoop ever executed.
type ConfidenceObservation struct {
	Source      string   `json:"source"`
	Status      string   `json:"status"`
	StartedAt   string   `json:"started_at"`
	CompletedAt string   `json:"completed_at,omitempty"`
	Level       string   `json:"level,omitempty"`
	Score       *float64 `json:"score,omitempty"`
	Reasoning   string   `json:"reasoning,omitempty"`
}
type RuntimeStatusSnapshot struct {
	Plan                 *PlanExecutionObservation       `json:"plan,omitempty"`
	Iterative            *IterativeEvaluationObservation `json:"iterative,omitempty"`
	Version              int                             `json:"version"`
	SessionID            string                          `json:"session_id"`
	Verification         VerificationStatus              `json:"verification"`
	Evaluation           *ConfidenceObservation          `json:"evaluation"`
	PersistenceAvailable bool                            `json:"persistence_available"`
	PersistenceFailed    bool                            `json:"persistence_failed"`
}

func cloneConfidence(value *ConfidenceObservation) *ConfidenceObservation {
	if value == nil {
		return nil
	}
	copy := *value
	if value.Score != nil {
		score := *value.Score
		copy.Score = &score
	}
	return &copy
}
func (a *Agent) RuntimeStatusSnapshot() RuntimeStatusSnapshot {
	state := &a.verificationObservations
	state.mu.Lock()
	defer state.mu.Unlock()
	return RuntimeStatusSnapshot{Plan: clonePlanExecution(state.plan), Iterative: cloneIterative(state.iterative), Version: 1, SessionID: a.SessionID(), Verification: summarizeVerification(state.checks, state.dropped), Evaluation: cloneConfidence(state.evaluation), PersistenceAvailable: state.persist != nil, PersistenceFailed: state.persistFailed}
}

// RecoverRuntimeStatus never represents old process work as still running.
func RecoverRuntimeStatus(snapshot RuntimeStatusSnapshot) RuntimeStatusSnapshot {
	snapshot.Verification.Tests = append([]VerificationCheck{}, snapshot.Verification.Tests...)
	for i := range snapshot.Verification.Tests {
		if snapshot.Verification.Tests[i].Status == "running" {
			snapshot.Verification.Tests[i].Status = "unconfirmed"
			snapshot.Verification.Tests[i].Passed = false
			snapshot.Verification.Tests[i].Error = "Persisted in-flight observation; this server cannot establish whether its originating runtime is still active."
		}
	}
	snapshot.Verification = summarizeVerification(snapshot.Verification.Tests, snapshot.Verification.Dropped)
	snapshot.Plan = clonePlanExecution(snapshot.Plan)
	if snapshot.Plan != nil {
		if snapshot.Plan.Status == "running" {
			snapshot.Plan.Status = "unconfirmed"
		}
		for i := range snapshot.Plan.Phases {
			if snapshot.Plan.Phases[i].Status == "running" {
				snapshot.Plan.Phases[i].Status = "unconfirmed"
			}
		}
	}
	snapshot.Iterative = cloneIterative(snapshot.Iterative)
	if snapshot.Iterative != nil && snapshot.Iterative.Status == "running" {
		snapshot.Iterative.Status = "unconfirmed"
	}
	snapshot.Evaluation = cloneConfidence(snapshot.Evaluation)
	if snapshot.Evaluation != nil && snapshot.Evaluation.Status == "running" {
		snapshot.Evaluation.Status = "unconfirmed"
	}
	return snapshot
}
func ValidateRuntimeStatus(snapshot RuntimeStatusSnapshot, sessionID string) error {
	if snapshot.Verification.Source != "post_write_verifier" || snapshot.Version != 1 || snapshot.SessionID != sessionID || len(snapshot.Verification.Tests) > 100 || snapshot.Verification.Dropped < 0 {
		return fmt.Errorf("agent: invalid runtime status")
	}
	for _, check := range snapshot.Verification.Tests {
		if len(check.StartedAt) > 64 || check.ID == "" || check.Passed != (check.Status == "passed") || len(check.ID) > 128 || len(check.Name) > 512 || len(check.File) > 4096 || len(check.Error) > 8192 {
			return fmt.Errorf("agent: oversized check")
		}
		switch check.Status {
		case "running", "passed", "failed", "skipped", "canceled", "unconfirmed":
		default:
			return fmt.Errorf("agent: invalid check status")
		}
	}
	if p := snapshot.Plan; p != nil {
		if p.Source != "loaded_plan_executor" || len(p.Title) > 512 || len(p.Phases) < 1 || len(p.Phases) > 100 {
			return fmt.Errorf("agent: invalid plan execution")
		}
		switch p.Status {
		case "running", "completed", "deferred", "failed", "unconfirmed":
		default:
			return fmt.Errorf("agent: invalid plan execution status")
		}
		for _, phase := range p.Phases {
			if len(phase.Title) > 512 || len(phase.Note) > 2048 {
				return fmt.Errorf("agent: oversized plan phase")
			}
			switch phase.Status {
			case "pending", "running", "done", "failed", "skipped", "deferred", "unconfirmed":
			default:
				return fmt.Errorf("agent: invalid plan phase status")
			}
			if p.Status == "completed" && phase.Status != "done" {
				return fmt.Errorf("agent: incomplete plan reported completed")
			}
		}
	}
	if e := snapshot.Iterative; e != nil {
		if e.Source != "iterative_self_evaluation" || e.Iteration < 1 || e.MaxIterations < 1 || e.Iteration > e.MaxIterations || len(e.StartedAt) > 64 || len(e.CompletedAt) > 64 || len(e.Note) > 2048 {
			return fmt.Errorf("agent: invalid iterative evaluation")
		}
		switch e.Status {
		case "running", "accepted", "deferred", "failed", "unconfirmed":
		default:
			return fmt.Errorf("agent: invalid iterative status")
		}
		switch e.Phase {
		case "executing", "assessing", "red_team", "complete":
		default:
			return fmt.Errorf("agent: invalid iterative phase")
		}
	}
	if e := snapshot.Evaluation; e != nil {
		if e.Source != "heuristic_confidence" || len(e.StartedAt) > 64 || len(e.CompletedAt) > 64 || len(e.Level) > 32 || len(e.Reasoning) > 2048 {
			return fmt.Errorf("agent: invalid evaluation")
		}
		if e.Score != nil && (math.IsNaN(*e.Score) || math.IsInf(*e.Score, 0) || *e.Score < 0 || *e.Score > 1) {
			return fmt.Errorf("agent: invalid confidence")
		}
		switch e.Status {
		case "running", "assessed", "failed", "unconfirmed":
		default:
			return fmt.Errorf("agent: invalid assessment status")
		}
	}
	return nil
}
func (a *Agent) SetRuntimeStatusPersistence(restored *RuntimeStatusSnapshot, persist func(context.Context, RuntimeStatusSnapshot) error) error {
	if restored != nil {
		if err := ValidateRuntimeStatus(*restored, a.SessionID()); err != nil {
			return err
		}
	}
	state := &a.verificationObservations
	state.mu.Lock()
	defer state.mu.Unlock()
	if restored != nil {
		snapshot := RecoverRuntimeStatus(*restored)
		state.checks = append([]VerificationCheck(nil), snapshot.Verification.Tests...)
		state.dropped = snapshot.Verification.Dropped
		state.evaluation = cloneConfidence(snapshot.Evaluation)
		state.iterative = cloneIterative(snapshot.Iterative)
		state.plan = clonePlanExecution(snapshot.Plan)
	}
	state.persist = persist
	return nil
}
func (a *Agent) persistRuntimeStatus() {
	state := &a.verificationObservations
	state.persistMu.Lock()
	defer state.persistMu.Unlock()
	state.mu.Lock()
	persist := state.persist
	state.mu.Unlock()
	if persist == nil {
		return
	}
	snapshot := a.RuntimeStatusSnapshot()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := persist(ctx, snapshot)
	state.mu.Lock()
	state.persistFailed = err != nil
	state.mu.Unlock()
}
func (a *Agent) startConfidenceObservation() {
	state := &a.verificationObservations
	state.mu.Lock()
	state.evaluation = &ConfidenceObservation{Source: "heuristic_confidence", Status: "running", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	state.mu.Unlock()
	a.persistRuntimeStatus()
}
func (a *Agent) finishConfidenceObservation(assessment *ConfidenceAssessment) {
	state := &a.verificationObservations
	state.mu.Lock()
	e := cloneConfidence(state.evaluation)
	if e == nil {
		e = &ConfidenceObservation{Source: "heuristic_confidence"}
	}
	e.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	e.Status = "failed"
	if assessment != nil {
		e.Status = "assessed"
		e.Level = assessment.Level.String()
		score := assessment.Score
		e.Score = &score
		e.Reasoning, _ = boundedVerificationText(assessment.Reasoning, 2048)
	}
	state.evaluation = e
	state.mu.Unlock()
	a.persistRuntimeStatus()
	a.emit("confidence_assessment", map[string]any{"session_id": a.SessionID(), "assessment": cloneConfidence(e)})
}
