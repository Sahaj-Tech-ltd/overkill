package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type resumedPlanProvider struct{ requests []providers.Request }

func (p *resumedPlanProvider) Name() string              { return "fixture" }
func (p *resumedPlanProvider) Models() []providers.Model { return nil }
func (p *resumedPlanProvider) response(request providers.Request) providers.Response {
	step := len(p.requests)
	p.requests = append(p.requests, request)
	if step == 0 || step == 2 {
		path := "first.txt"
		if step == 2 {
			path = "second.txt"
		}
		args, _ := json.Marshal(map[string]string{"action": "write", "path": path, "content": "done"})
		return providers.Response{ToolCalls: []providers.ToolCall{{ID: path, Name: "fs", Arguments: string(args)}}}
	}
	return providers.Response{Content: "Phase completed with actual file output."}
}
func (p *resumedPlanProvider) Complete(_ context.Context, request providers.Request) (providers.Response, error) {
	return p.response(request), nil
}
func (p *resumedPlanProvider) Stream(_ context.Context, request providers.Request) (<-chan providers.Chunk, error) {
	response := p.response(request)
	out := make(chan providers.Chunk, 2)
	out <- providers.Chunk{Content: response.Content, ToolCalls: response.ToolCalls}
	out <- providers.Chunk{Done: true}
	close(out)
	return out, nil
}

func TestDurableLoadedPlanResumesActiveAndNotStartedPhasesAcrossFreshRuntimes(t *testing.T) {
	q, _, state := flowQueueFixture(t)
	p := &resumedPlanProvider{}
	factory := func() *Agent {
		registry := tools.NewRegistry()
		require.NoError(t, registry.Register(tools.NewFSTool(state.Workspace)))
		a := New(Config{Provider: p, Model: "fixture", SessionID: state.SessionID, Workspace: state.Workspace, Tools: registry, MaxSteps: 1})
		a.SetAutoMode("auto")
		a.AutoMode().SelfEval.SetConfidenceFn(func(string, []providers.Message, string) *ConfidenceAssessment {
			return &ConfidenceAssessment{Score: .95}
		})
		a.SetFlowStore(q.Bind(state.SessionID, state.Workspace, "fixture", "fixture"), nil)
		return a
	}
	initial := factory()
	initial.AutoMode().Plan = &Plan{Title: "Two real files", Phases: []PlanPhase{{Index: 0, Title: "First file", Description: "Write first.txt"}, {Index: 1, Title: "Second file", Description: "Write second.txt"}}}
	_, err := initial.Run(t.Context(), "work")
	require.ErrorContains(t, err, "exceeded max steps")
	initial.Shutdown()
	jobs, err := q.ListSession(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	flowID := jobs[0].ID
	for attempt := 1; attempt <= 3; attempt++ {
		time.Sleep(3 * time.Millisecond)
		lease, err := q.Claim(t.Context(), flowID)
		require.NoError(t, err)
		require.NotNil(t, lease)
		require.Equal(t, attempt, lease.State.Resumes)
		require.NotNil(t, lease.State.LoadedPlan)
		if attempt == 2 {
			require.False(t, lease.State.LoadedPlan.Started)
			require.Equal(t, 1, lease.State.LoadedPlan.CurrentPhase)
			require.Equal(t, PhaseDone, lease.State.LoadedPlan.Plan.Phases[0].Status)
		}
		a := factory()
		events, err := a.ContinueStream(t.Context(), lease.State)
		require.NoError(t, err)
		complete, checkpointed := false, false
		for event := range events {
			if event.Error != nil {
				require.True(t, errors.Is(event.Error, errStepBudget), "unexpected failure: %v", event.Error)
			}
			if event.Type == EventDone {
				_, checkpointed = event.Metadata["flow_checkpoint"]
				complete = !checkpointed
			}
		}
		if complete {
			require.NoError(t, lease.Finish(t.Context(), "completed", ""))
			require.Equal(t, "completed", a.RuntimeStatusSnapshot().Plan.Status)
		} else {
			require.NoError(t, lease.Finish(t.Context(), "manual_review", "older continuation ended"))
		}
		a.Shutdown()
		lease.Close()
	}
	jobs, err = q.ListSession(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "completed", jobs[0].Status)
	require.Len(t, p.requests, 4)
	for _, path := range []string{"first.txt", "second.txt"} {
		raw, err := os.ReadFile(filepath.Join(state.Workspace, path))
		require.NoError(t, err)
		require.Equal(t, "done", string(raw))
	}
	originalCount := 0
	for _, message := range p.requests[3].Messages {
		if message.Role == "user" && strings.HasPrefix(message.Content, "work\n\nLoaded plan:") {
			originalCount++
		}
	}
	require.Equal(t, 1, originalCount, "original user/phase instruction must not be reappended on continuation")
}

func TestLoadedFlowPlanRestoresEvaluationWithoutEscalatingCurrentToolAuthority(t *testing.T) {
	a := New(Config{Provider: &flowDoneProvider{}, Model: "fixture", SessionID: "s", MaxSteps: 2})
	defer a.Shutdown()
	a.SetAutoMode("safe")
	state := &FlowState{SessionID: "s", Model: "fixture", UserInput: "original", History: []providers.Message{{Role: "user", Content: "original"}}, LoadedPlan: &LoadedFlowPlan{Plan: Plan{Title: "one", Phases: []PlanPhase{{Index: 0, Title: "one", Status: PhaseRunning}}}, CurrentPhase: 0, Started: true, RequireEvaluation: true, MinConfidence: .8, MaxIterations: 2, MaxRevisionDepth: 1}}
	a.RestoreHistory(state.History)
	_, err := a.restoreFlowPlan(t.Context(), state)
	require.NoError(t, err)
	require.True(t, a.AutoMode().IsSafe())
	require.False(t, a.AutoMode().IsAuto())
	require.True(t, a.AutoMode().ShouldSelfEvaluate())
	require.Equal(t, .8, a.AutoMode().SelfEval.MinConfidence)
	require.False(t, a.checkToolApproval("shell", `{"command":"touch x"}`))
}

func TestContinueStreamRejectsOverlapBeforeChangingAdmittedHistory(t *testing.T) {
	provider := &deadlineFlowProvider{entered: make(chan struct{})}
	a := New(Config{Provider: provider, Model: "fixture", SessionID: "s", MaxSteps: 2})
	defer a.Shutdown()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := a.Run(ctx, "active original"); done <- err }()
	select {
	case <-provider.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("active run did not start")
	}
	before := a.History()
	_, err := a.ContinueStream(t.Context(), &FlowState{SessionID: "s", Model: "fixture", History: []providers.Message{{Role: "user", Content: "replace admitted history"}}})
	require.ErrorContains(t, err, "already running")
	require.Equal(t, before, a.History())
	cancel()
	require.Error(t, <-done)
}
func TestCorruptFlowPlanCannotReplaceCachedHistory(t *testing.T) {
	a := New(Config{Provider: &flowDoneProvider{}, Model: "fixture", SessionID: "s", MaxSteps: 2})
	defer a.Shutdown()
	a.RestoreHistory([]providers.Message{{Role: "user", Content: "valid stored conversation"}})
	_, err := a.ContinueStream(t.Context(), &FlowState{SessionID: "s", Model: "fixture", ReadOnly: true, History: []providers.Message{{Role: "user", Content: "corrupt replacement"}}, LoadedPlan: &LoadedFlowPlan{CurrentPhase: 7}})
	require.Error(t, err)
	require.Equal(t, "valid stored conversation", a.History()[0].Content)
	require.NotEqual(t, security.ModeReader, a.PrivilegeMode())
	require.False(t, a.IsRunning())
}
