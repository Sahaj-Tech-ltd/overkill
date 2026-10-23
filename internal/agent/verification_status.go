package agent

import (
	"context"
	"github.com/google/uuid"
	"strings"
	"sync"
	"time"
)

// VerificationCheck records an actually invoked verifier, never an inferred
// success from empty feedback. Skipped and canceled checks are not passes.
type VerificationCheck struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	File            string `json:"file"`
	Status          string `json:"status"`
	Passed          bool   `json:"passed"`
	Error           string `json:"error,omitempty"`
	DurationMS      int64  `json:"duration_ms"`
	Category        string `json:"category"`
	StartedAt       string `json:"started_at"`
	OutputTruncated bool   `json:"output_truncated"`
}
type VerificationStatus struct {
	Source      string              `json:"source"`
	Running     bool                `json:"running"`
	Tests       []VerificationCheck `json:"tests"`
	Total       int                 `json:"total"`
	Passed      int                 `json:"passed"`
	Failed      int                 `json:"failed"`
	Skipped     int                 `json:"skipped"`
	Canceled    int                 `json:"canceled"`
	Unconfirmed int                 `json:"unconfirmed"`
	Dropped     int                 `json:"dropped"`
}
type verificationObservations struct {
	mu            sync.Mutex
	checks        []VerificationCheck
	dropped       int
	evaluation    *ConfidenceObservation
	plan          *PlanExecutionObservation
	iterative     *IterativeEvaluationObservation
	persistMu     sync.Mutex
	persist       func(context.Context, RuntimeStatusSnapshot) error
	persistFailed bool
}
type verificationObserverKey struct{}

func (a *Agent) observedVerificationContext(ctx context.Context) context.Context {
	return context.WithValue(verificationContext(ctx), verificationObserverKey{}, a.recordVerification)
}

// StartVerification is called by the wiring adapter immediately before an
// actual checker invocation. The returned callback must be called exactly once.
func StartVerification(ctx context.Context, name, path string) func(status, note string) {
	observer, _ := ctx.Value(verificationObserverKey{}).(func(VerificationCheck))
	if observer == nil {
		return func(string, string) {}
	}
	started := time.Now()
	check := VerificationCheck{ID: uuid.NewString(), Name: name, File: path, Status: "running", Category: "post_write", StartedAt: started.UTC().Format(time.RFC3339Nano)}
	observer(check)
	var once sync.Once
	return func(status, note string) {
		once.Do(func() {
			check.Status = status
			check.Passed = status == "passed"
			check.Error = note
			check.DurationMS = time.Since(started).Milliseconds()
			observer(check)
		})
	}
}
func boundedVerificationText(value string, limit int) (string, bool) {
	truncated := len(value) > limit
	if truncated {
		value = value[:limit]
	}
	clean := strings.ToValidUTF8(value, "")
	// Clone avoids retaining a multi-megabyte diagnostic backing buffer.
	return strings.Clone(clean), truncated || clean != value
}

func (a *Agent) recordVerification(check VerificationCheck) {
	var nameTruncated, fileTruncated, noteTruncated bool
	check.Name, nameTruncated = boundedVerificationText(check.Name, 512)
	check.File, fileTruncated = boundedVerificationText(check.File, 4096)
	check.Error, noteTruncated = boundedVerificationText(check.Error, 8192)
	check.OutputTruncated = check.OutputTruncated || nameTruncated || fileTruncated || noteTruncated
	state := &a.verificationObservations
	state.mu.Lock()
	found := false
	for i := range state.checks {
		if state.checks[i].ID == check.ID {
			state.checks[i] = check
			found = true
			break
		}
	}
	if !found {
		if len(state.checks) >= 100 {
			state.checks = append([]VerificationCheck(nil), state.checks[1:]...)
			state.dropped++
		}
		state.checks = append(state.checks, check)
	}
	state.mu.Unlock()
	a.emit("verification", map[string]any{"session_id": a.SessionID(), "check": check})
	a.persistRuntimeStatus()
}
func (a *Agent) VerificationSnapshot() VerificationStatus {
	state := &a.verificationObservations
	state.mu.Lock()
	defer state.mu.Unlock()
	return summarizeVerification(state.checks, state.dropped)
}
func summarizeVerification(checks []VerificationCheck, dropped int) VerificationStatus {
	result := VerificationStatus{Source: "post_write_verifier", Tests: append([]VerificationCheck{}, checks...), Dropped: dropped, Total: len(checks)}
	for _, check := range result.Tests {
		switch check.Status {
		case "running":
			result.Running = true
		case "passed":
			result.Passed++
		case "failed":
			result.Failed++
		case "skipped":
			result.Skipped++
		case "canceled":
			result.Canceled++
		case "unconfirmed":
			result.Unconfirmed++
		}
	}
	return result
}
