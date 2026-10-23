package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func fixtureLoadedPlan(a *Agent) {
	a.SetAutoMode("auto")
	a.AutoMode().Plan = &Plan{Title: "Two phases", Phases: []PlanPhase{
		{Index: 0, Title: "Explain first", Description: "first details"},
		{Index: 1, Title: "Explain second", Description: "second details"},
	}}
	a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
		return &ConfidenceAssessment{Score: .9}
	})
}

func TestLoadedPlanProductionCallersShareGoalAdmissionAndUsage(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "Run", true: "Stream"}[streaming], func(t *testing.T) {
			gs := newExecutionGoalStore(t)
			sid := "loaded-plan"
			require.NoError(t, gs.SetWithBudgets(t.Context(), sid, "execute plan", 100, 0))
			usage := providers.Usage{InputTokens: 3, OutputTokens: 2}
			var a *Agent
			var prompts []string
			check := func(req providers.Request) {
				_, err := a.Run(t.Context(), "overlap")
				require.ErrorContains(t, err, "already running")
				prompts = append(prompts, req.Messages[len(req.Messages)-1].Content)
			}
			p := &goalTestProvider{
				complete: func(_ context.Context, req providers.Request) (providers.Response, error) {
					check(req)
					return providers.Response{Content: "phase result", Usage: usage}, nil
				},
				stream: func(_ context.Context, req providers.Request) (<-chan providers.Chunk, error) {
					check(req)
					return goalChunks(providers.Chunk{Content: "first result", Usage: &usage, Done: true})(t.Context(), req)
				},
			}
			a = goalAgent(p, gs, sid)
			defer a.Shutdown()
			fixtureLoadedPlan(a)
			var snapshots []RuntimeStatusSnapshot
			require.NoError(t, a.SetRuntimeStatusPersistence(nil, func(_ context.Context, s RuntimeStatusSnapshot) error { snapshots = append(snapshots, s); return nil }))
			var r *RunResult
			var err error
			if streaming {
				r, err = goalStream(t.Context(), a)
			} else {
				r, err = a.Run(t.Context(), "work")
			}
			require.NoError(t, err)
			require.NotNil(t, r)
			require.False(t, r.Blocked)
			require.Len(t, prompts, 2)
			require.Contains(t, prompts[0], "ONLY phase 1 of 2")
			require.Contains(t, prompts[1], "ONLY phase 2 of 2")
			require.Equal(t, 2, r.Steps)
			require.Equal(t, 10, r.TotalTokens)
			require.Equal(t, "completed", r.Plan.Status)
			goal := requireGoal(t, gs, sid)
			require.Equal(t, 10, goal.TokensUsed)
			var turns int
			require.NoError(t, gs.db.QueryRow(`SELECT COUNT(*) FROM goal_turn_usage WHERE goal_id=$1`, goal.ID).Scan(&turns))
			require.Equal(t, 1, turns)
			require.Equal(t, "done", a.RuntimeStatusSnapshot().Plan.Phases[0].Status)
			for _, s := range snapshots {
				require.NoError(t, ValidateRuntimeStatus(s, a.SessionID()))
			}
			require.Greater(t, len(snapshots), 3)
			recovered := RecoverRuntimeStatus(snapshots[0])
			require.Equal(t, "unconfirmed", recovered.Plan.Status)
			require.Equal(t, "running", snapshots[0].Plan.Status)
			r.Plan.Phases[0].Status = "mutated"
			require.Equal(t, "done", a.RuntimeStatusSnapshot().Plan.Phases[0].Status)
		})
	}
}

func TestLoadedPlanStopsBeforeRemainingPhases(t *testing.T) {
	for _, reason := range []string{"deferred", "provider_error", "cancel", "steps"} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p := &goalTestProvider{complete: func(_ context.Context, _ providers.Request) (providers.Response, error) {
				if reason == "provider_error" {
					return providers.Response{}, errors.New("provider offline")
				}
				return providers.Response{Content: "result"}, nil
			}}
			a := goalAgent(p, nil, "loaded-plan-failure")
			defer a.Shutdown()
			fixtureLoadedPlan(a)
			switch reason {
			case "deferred":
				a.AutoMode().SelfEval.MaxIterations = 1
				a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
					return &ConfidenceAssessment{Score: .1}
				})
			case "cancel":
				a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
					cancel()
					return &ConfidenceAssessment{Score: .9}
				})
			case "steps":
				a.maxSteps = 1
			}
			r, err := a.Run(ctx, "work")
			if reason == "deferred" {
				require.NoError(t, err)
				require.True(t, r.Blocked)
			} else {
				require.Error(t, err)
			}
			status := a.RuntimeStatusSnapshot().Plan
			require.NotEqual(t, "completed", status.Status)
			if reason == "steps" {
				require.Equal(t, "done", status.Phases[0].Status)
			} else {
				require.NotEqual(t, "done", status.Phases[0].Status)
				require.Equal(t, "pending", status.Phases[1].Status)
			}
			require.Equal(t, int32(1), p.calls.Load())
			require.NoError(t, ValidateRuntimeStatus(a.RuntimeStatusSnapshot(), a.SessionID()))
		})
	}
}

func TestBuildCommandExecutesExplicitPlanThroughBothCallers(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "Run", true: "Stream"}[streaming], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "Mixed Case Plan.md")
			require.NoError(t, os.WriteFile(path, []byte("# Explain plan\n## Explain first\nFirst details\n## Explain second\nSecond details\n"), 0600))
			p := &mockProvider{responses: []providers.Response{{Content: "answer one"}, {Content: "answer two"}}}
			a := newTestAgent(p, nil, nil, nil)
			defer a.Shutdown()
			// The default explanatory confidence policy may revise, so allow enough
			// steps; assertions concern real progression and truthful terminal state.
			var r *RunResult
			if streaming {
				ch, err := a.Stream(t.Context(), "/build "+path)
				require.NoError(t, err)
				for e := range ch {
					require.NoError(t, e.Error)
					if e.Result != nil {
						r = e.Result
					}
				}
			} else {
				var err error
				r, err = a.Run(t.Context(), "/build "+path)
				require.NoError(t, err)
			}
			require.NotNil(t, r)
			require.NotNil(t, r.Plan)
			require.Equal(t, "completed", r.Plan.Status)
			require.GreaterOrEqual(t, p.callCount, 2)
			require.True(t, strings.Contains(a.AutoMode().Plan.Source, "Mixed Case Plan.md"))
		})
	}
}

func TestLoadedPlanToolStepsShareTheTotalBound(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "Run", true: "Stream"}[streaming], func(t *testing.T) {
			calls := 0
			registry := tools.NewRegistry()
			registry.Register(&mockTool{name: "phase_work", execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				calls++
				return json.RawMessage(`{"done":true}`), nil
			}})
			p := &mockProvider{responses: []providers.Response{
				{ToolCalls: []providers.ToolCall{{ID: "phase-one-tool", Name: "phase_work", Arguments: `{}`}}},
				{Content: "first phase result"},
				{Content: "second phase result"},
			}}
			a := newTestAgent(p, registry, nil, nil)
			defer a.Shutdown()
			fixtureLoadedPlan(a)
			a.maxSteps = 3
			var result *RunResult
			var err error
			if streaming {
				result, err = goalStream(t.Context(), a)
			} else {
				result, err = a.Run(t.Context(), "work")
			}
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Equal(t, 3, result.Steps)
			require.Equal(t, 1, result.ToolCalls)
			require.Equal(t, "completed", result.Plan.Status)
		})
	}
}

func TestLoadedPlanRequiresAutoAndExplicitReloadAfterFailure(t *testing.T) {
	for _, level := range []AutonomyLevel{AutonomySafe, AutonomyYolo} {
		t.Run(string(level), func(t *testing.T) {
			p := &mockProvider{responses: []providers.Response{{Content: "ordinary answer"}}}
			a := newTestAgent(p, nil, nil, nil)
			defer a.Shutdown()
			fixtureLoadedPlan(a)
			a.AutoMode().Level = level
			result, err := a.Run(t.Context(), "work")
			require.NoError(t, err)
			require.Equal(t, 1, result.Steps)
			require.Nil(t, a.RuntimeStatusSnapshot().Plan)
			require.Equal(t, PhasePending, a.AutoMode().Plan.Phases[0].Status)
		})
	}
	p := &mockProvider{responses: []providers.Response{{Content: "first answer"}, {Content: "ordinary next turn"}}}
	a := newTestAgent(p, nil, nil, nil)
	defer a.Shutdown()
	fixtureLoadedPlan(a)
	a.AutoMode().SelfEval.MaxIterations = 1
	a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
		return &ConfidenceAssessment{Score: .1}
	})
	result, err := a.Run(t.Context(), "work")
	require.NoError(t, err)
	require.True(t, result.Blocked)
	a.AutoMode().SetSelfEval(nil)
	_, err = a.Run(t.Context(), "new unrelated question")
	require.NoError(t, err)
	require.Equal(t, "deferred", a.RuntimeStatusSnapshot().Plan.Status)
	require.Equal(t, PhasePending, a.AutoMode().Plan.Phases[1].Status)
	require.Equal(t, 2, p.callCount)
}

func TestLoadedPlanCompletionNotificationsDescribeActualTerminalOutcome(t *testing.T) {
	for _, outcome := range []string{"accepted", "deferred", "steps", "provider_error"} {
		t.Run(outcome, func(t *testing.T) {
			p := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
				if outcome == "provider_error" {
					return providers.Response{}, errors.New("provider offline")
				}
				return providers.Response{Content: "phase answer"}, nil
			}}
			a := goalAgent(p, nil, "plan-notification")
			defer a.Shutdown()
			fixtureLoadedPlan(a)
			if outcome == "deferred" {
				a.AutoMode().SelfEval.MaxIterations = 1
				a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
					return &ConfidenceAssessment{Score: .1}
				})
			}
			if outcome == "steps" {
				a.maxSteps = 1
			}
			var notifications []*SelfEvalResult
			a.AutoMode().SetCompletionFn(func(result *SelfEvalResult) { notifications = append(notifications, result) })
			_, err := a.Run(t.Context(), "work")
			if outcome == "steps" || outcome == "provider_error" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, notifications, 1)
			require.Equal(t, outcome == "accepted", notifications[0].Accepted)
			require.NotEmpty(t, notifications[0].FinalNote)
			plan := a.RuntimeStatusSnapshot().Plan
			if outcome == "provider_error" || outcome == "deferred" {
				require.Equal(t, "pending", plan.Phases[1].Status)
				require.Contains(t, plan.Phases[1].Note, "Not started")
			}
			if outcome == "steps" {
				require.Equal(t, "deferred", plan.Phases[1].Status)
				require.Contains(t, plan.Phases[1].Note, "not started")
			}
		})
	}
}

func TestLoadedPlanFileInputIsBoundedBeforeParsing(t *testing.T) {
	root := t.TempDir()
	_, err := LoadPlan(root)
	require.ErrorContains(t, err, "regular file")
	path := filepath.Join(root, "large.md")
	require.NoError(t, os.WriteFile(path, []byte("## Explain phase\n"+strings.Repeat("x", MaxPlanBytes)), 0600))
	_, err = LoadPlan(path)
	require.ErrorContains(t, err, "at most")
	require.NoError(t, os.WriteFile(path, []byte("## Explain phase\nSmall complete instruction."), 0600))
	plan, err := LoadPlan(path)
	require.NoError(t, err)
	require.Equal(t, "Small complete instruction.", plan.Phases[0].Description)
}
