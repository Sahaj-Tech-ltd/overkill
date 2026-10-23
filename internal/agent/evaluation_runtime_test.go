package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestIterativeEvaluationProductionCallers(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "Run"
		if streaming {
			name = "Stream"
		}
		t.Run(name, func(t *testing.T) {
			p := &mockProvider{responses: []providers.Response{{Content: "initial answer"}, {Content: "revised answer"}}}
			a := newTestAgent(p, nil, nil, nil)
			defer a.Shutdown()
			a.SetAutoMode("auto")
			calls := 0
			a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
				calls++
				score := 0.1
				if calls == 2 {
					score = 0.9
				}
				return &ConfidenceAssessment{Score: score}
			})
			var snapshots []RuntimeStatusSnapshot
			require.NoError(t, a.SetRuntimeStatusPersistence(nil, func(_ context.Context, snapshot RuntimeStatusSnapshot) error {
				snapshots = append(snapshots, snapshot)
				return nil
			}))
			var result *RunResult
			var err error
			if streaming {
				result, err = goalStream(t.Context(), a)
			} else {
				result, err = a.Run(t.Context(), "work")
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.Blocked)
			require.Equal(t, "revised answer", result.Response)
			require.Equal(t, 2, result.Steps)
			require.Equal(t, 2, p.callCount)
			status := a.RuntimeStatusSnapshot()
			require.Equal(t, "accepted", status.Iterative.Status)
			require.Equal(t, 2, status.Iterative.Iteration)
			require.NoError(t, ValidateRuntimeStatus(status, a.SessionID()))
			var executing, assessing bool
			for _, snapshot := range snapshots {
				if snapshot.Iterative == nil {
					continue
				}
				executing = executing || snapshot.Iterative.Phase == "executing"
				assessing = assessing || snapshot.Iterative.Phase == "assessing"
				require.NoError(t, ValidateRuntimeStatus(snapshot, a.SessionID()))
			}
			require.True(t, executing)
			require.True(t, assessing)
			encoded, err := json.Marshal(status)
			require.NoError(t, err)
			var restored RuntimeStatusSnapshot
			require.NoError(t, json.Unmarshal(encoded, &restored))
			other := newTestAgent(&mockProvider{}, nil, nil, nil)
			defer other.Shutdown()
			require.NoError(t, other.SetRuntimeStatusPersistence(&restored, nil))
			require.Equal(t, "accepted", other.RuntimeStatusSnapshot().Iterative.Status)
			status.Iterative.Status = "mutated"
			require.Equal(t, "accepted", a.RuntimeStatusSnapshot().Iterative.Status)
		})
	}
}

func TestIterativeEvaluationRequiresOptIn(t *testing.T) {
	for _, mode := range []string{"", "safe", "yolo", "supervised", "auto-disabled"} {
		t.Run(mode, func(t *testing.T) {
			p := &mockProvider{responses: []providers.Response{{Content: "answer"}}}
			a := newTestAgent(p, nil, nil, nil)
			defer a.Shutdown()
			if mode == "auto-disabled" {
				a.SetAutoMode("auto")
				a.AutoMode().SetSelfEval(nil)
			} else {
				a.SetAutoMode(mode)
			}
			result, err := a.Run(t.Context(), "work")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 1, p.callCount)
			require.Nil(t, a.RuntimeStatusSnapshot().Iterative)
		})
	}
}

func TestIterativeEvaluationDeferralAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		name := "deferred"
		if cancel {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			p := &mockProvider{responses: []providers.Response{{Content: "answer"}}}
			a := newTestAgent(p, nil, nil, nil)
			defer a.Shutdown()
			a.SetAutoMode("auto")
			loop := a.AutoMode().SelfEval
			loop.MaxIterations = 2
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			loop.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
				if cancel {
					stop()
				}
				return &ConfidenceAssessment{Score: 0.1}
			})
			result, err := a.Run(ctx, "work")
			status := a.RuntimeStatusSnapshot().Iterative
			if cancel {
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, "failed", status.Status)
			} else {
				require.NoError(t, err)
				require.True(t, result.Blocked)
				require.Equal(t, "deferred", status.Status)
			}
			require.NotEmpty(t, status.CompletedAt)
		})
	}
}

func TestIterativeEvaluationStepLimitAndPersistenceFailure(t *testing.T) {
	a := newTestAgent(&mockProvider{responses: []providers.Response{{Content: "answer"}}}, nil, nil, nil)
	defer a.Shutdown()
	a.SetAutoMode("auto")
	a.maxSteps = 1
	a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
		return &ConfidenceAssessment{Score: 0.1}
	})
	require.NoError(t, a.SetRuntimeStatusPersistence(nil, func(context.Context, RuntimeStatusSnapshot) error { return errors.New("storage unavailable") }))
	_, err := a.Run(t.Context(), "work")
	require.ErrorContains(t, err, "max steps")
	snapshot := a.RuntimeStatusSnapshot()
	require.True(t, snapshot.PersistenceFailed)
	require.Equal(t, "failed", snapshot.Iterative.Status)
	inFlight := snapshot
	inFlight.Iterative = cloneIterative(snapshot.Iterative)
	inFlight.Iterative.Status = "running"
	require.Equal(t, "unconfirmed", RecoverRuntimeStatus(inFlight).Iterative.Status)
	require.Equal(t, "running", inFlight.Iterative.Status)
}

func TestIterativeEvaluationSharesGoalLedgerAndAdmission(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "Run"
		if streaming {
			name = "Stream"
		}
		t.Run(name, func(t *testing.T) {
			gs := newExecutionGoalStore(t)
			sid := "self-evaluation-goal"
			require.NoError(t, gs.SetWithBudgets(t.Context(), sid, "complete task", 100, 0))
			usage := providers.Usage{InputTokens: 3, OutputTokens: 2}
			p := &goalTestProvider{
				complete: func(context.Context, providers.Request) (providers.Response, error) {
					return providers.Response{Content: "revision", Usage: usage}, nil
				},
				stream: goalChunks(providers.Chunk{Content: "initial", Usage: &usage, Done: true}),
			}
			a := goalAgent(p, gs, sid)
			defer a.Shutdown()
			a.SetAutoMode("auto")
			calls := 0
			a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
				calls++
				if calls == 1 {
					_, err := a.Run(t.Context(), "overlap")
					require.ErrorContains(t, err, "already running")
					return &ConfidenceAssessment{Score: 0.1}
				}
				return &ConfidenceAssessment{Score: 0.9}
			})
			var result *RunResult
			var err error
			if streaming {
				result, err = goalStream(t.Context(), a)
			} else {
				result, err = a.Run(t.Context(), "work")
			}
			require.NoError(t, err)
			require.False(t, result.Blocked)
			require.Equal(t, int32(2), p.calls.Load())
			require.Equal(t, 10, result.TotalTokens)
			goal := requireGoal(t, gs, sid)
			require.Equal(t, 10, goal.TokensUsed)
			var turns int
			require.NoError(t, gs.db.QueryRow(`SELECT COUNT(*) FROM goal_turn_usage WHERE goal_id=$1`, goal.ID).Scan(&turns))
			require.Equal(t, 1, turns, "revisions belong to the admitted turn")
		})
	}
}

func TestIterativeEvaluationAcceptsCachedFirstAnswerWithoutExtraProviderCall(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "Run"
		if streaming {
			name = "Stream"
		}
		t.Run(name, func(t *testing.T) {
			p := &mockProvider{responses: []providers.Response{{Content: "first answer"}}}
			a := newTestAgent(p, nil, nil, nil)
			defer a.Shutdown()
			a.SetAutoMode("auto")
			assessments := 0
			a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
				assessments++
				return &ConfidenceAssessment{Score: 0.9}
			})
			var result *RunResult
			var err error
			if streaming {
				result, err = goalStream(t.Context(), a)
			} else {
				result, err = a.Run(t.Context(), "work")
			}
			require.NoError(t, err)
			require.False(t, result.Blocked)
			require.Equal(t, 1, assessments)
			require.Equal(t, 1, p.callCount)
			require.Equal(t, "first answer", result.Response)
			require.Equal(t, "accepted", a.RuntimeStatusSnapshot().Iterative.Status)
		})
	}
}

func TestIterativeEvaluationDefaultPolicyClassifiesActualTask(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "Run", true: "Stream"}[streaming], func(t *testing.T) {
			p := &mockProvider{responses: []providers.Response{{Content: "initial answer"}, {Content: "revised answer"}}}
			a := newTestAgent(p, nil, nil, nil)
			defer a.Shutdown()
			a.SetAutoMode("auto")
			var result *RunResult
			var err error
			if streaming {
				var events <-chan StreamEvent
				events, err = a.Stream(t.Context(), "Explain the review fixture.")
				require.NoError(t, err)
				for event := range events {
					require.NoError(t, event.Error)
					if event.Type == EventDone {
						result = event.Result
					}
				}
			} else {
				result, err = a.Run(t.Context(), "Explain the review fixture.")
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.Blocked)
			require.Equal(t, "revised answer", result.Response)
			require.Equal(t, 2, p.callCount)
			require.Equal(t, "accepted", a.RuntimeStatusSnapshot().Iterative.Status)
			require.Equal(t, 2, a.RuntimeStatusSnapshot().Iterative.Iteration)
		})
	}
}
