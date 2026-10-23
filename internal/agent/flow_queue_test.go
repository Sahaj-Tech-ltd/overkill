package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func flowQueueFixture(t *testing.T) (*FlowQueue, *SessionFlowStore, *FlowState) {
	t.Helper()
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	metadata := session.NewSession(workspace)
	metadata.Model = "fixture"
	metadata.Provider = "fixture"
	require.NoError(t, sessions.Create(t.Context(), metadata))
	queue, err := NewFlowQueue(db, time.Millisecond)
	require.NoError(t, err)
	bound := queue.Bind(metadata.ID, workspace, "fixture", "fixture")
	require.NoError(t, bound.BeginTurn(t.Context()))
	state := &FlowState{ID: "owned-flow", SessionID: metadata.ID, Model: "fixture", Provider: "fixture", Workspace: workspace, UserInput: "original task", History: []providers.Message{{Role: "user", Content: "original task"}, {Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "done-tool", Name: "fs", Arguments: `{"action":"write","path":"a"}`}}}, {Role: "tool", ToolCallID: "done-tool", Content: "completed"}}, Reason: "exceeded max steps (1)", CreatedAt: time.Now().UTC()}
	return queue, bound, state
}
func TestDurableFlowQueueAtomicTranscriptAndDuplicateLease(t *testing.T) {
	q, bound, state := flowQueueFixture(t)
	require.NoError(t, bound.Save(state))
	time.Sleep(3 * time.Millisecond)
	var count int
	require.NoError(t, q.db.QueryRow(`SELECT jsonb_array_length(messages) FROM sessions WHERE id=$1`, state.SessionID).Scan(&count))
	require.Len(t, state.History, count)
	lease, err := q.Claim(t.Context(), state.ID)
	require.NoError(t, err)
	require.NotNil(t, lease)
	defer lease.Close()
	require.Equal(t, 1, lease.State.Resumes)
	duplicate, err := q.Claim(t.Context(), state.ID)
	require.NoError(t, err)
	require.Nil(t, duplicate)
	state.Resumes = lease.State.Resumes
	state.History = append(state.History, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "next-tool", Name: "fs"}}}, providers.Message{Role: "tool", ToolCallID: "next-tool", Content: "more completed work"})
	require.NoError(t, bound.Save(state))
	require.NoError(t, lease.Finish(t.Context(), "completed", ""))
	lease.Close()
	preserved, err := q.store.Load(state.ID)
	require.NoError(t, err)
	require.NotNil(t, preserved)
	require.Equal(t, state.History, preserved.History)
	jobs, err := q.ListSession(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "queued", jobs[0].Status)
	require.EqualValues(t, 2, jobs[0].Generation)
	time.Sleep(3 * time.Millisecond)
	next, err := q.Claim(t.Context(), state.ID)
	require.NoError(t, err)
	require.NotNil(t, next)
	defer next.Close()
	require.Equal(t, 2, next.State.Resumes)
	require.NoError(t, next.Finish(t.Context(), "completed", ""))
	missing, err := q.store.Load(state.ID)
	require.NoError(t, err)
	require.Nil(t, missing)
}
func TestDurableFlowQueueExplicitStopFencesLateSaveAndNewTurnCanProceed(t *testing.T) {
	q, bound, state := flowQueueFixture(t)
	require.NoError(t, bound.Save(state))
	require.NoError(t, q.PauseSession(t.Context(), state.SessionID))
	require.ErrorContains(t, bound.Save(state), "superseded by explicit stop")
	jobs, err := q.ListSession(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, "paused", jobs[0].Status)
	time.Sleep(3 * time.Millisecond)
	lease, err := q.Claim(t.Context(), state.ID)
	require.NoError(t, err)
	require.Nil(t, lease)
	require.NoError(t, bound.BeginHumanTurn(t.Context()))
	require.NoError(t, bound.Save(state))
	jobs, err = q.ListSession(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, "queued", jobs[0].Status)
}
func TestDurableFlowQueueLostExecutingOwnerRequiresReview(t *testing.T) {
	q, bound, state := flowQueueFixture(t)
	require.NoError(t, bound.Save(state))
	time.Sleep(3 * time.Millisecond)
	lease, err := q.Claim(t.Context(), state.ID)
	require.NoError(t, err)
	require.NotNil(t, lease)
	lease.Close()
	replacement, err := q.Claim(t.Context(), state.ID)
	require.NoError(t, err)
	require.Nil(t, replacement)
	jobs, err := q.ListSession(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, "manual_review", jobs[0].Status)
}
func TestDurableFlowQueueDoesNotAutoResumeInterruptOrUnresolvedTool(t *testing.T) {
	for _, reason := range []string{"cancelled_by_user", "halted_by_estop", "exceeded max steps (1)"} {
		t.Run(reason, func(t *testing.T) {
			q, bound, state := flowQueueFixture(t)
			state.Reason = reason
			if reason == "exceeded max steps (1)" {
				state.History = state.History[:2]
			}
			require.NoError(t, bound.Save(state))
			jobs, err := q.ListSession(t.Context(), state.SessionID)
			require.NoError(t, err)
			if reason == "exceeded max steps (1)" {
				require.Equal(t, "manual_review", jobs[0].Status)
			} else {
				require.Equal(t, "paused", jobs[0].Status)
			}
		})
	}
}
func TestDurableFlowQueueCrossProcessSessionAdmission(t *testing.T) {
	q, _, state := flowQueueFixture(t)
	release, acquired, err := q.TrySessionOperation(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.True(t, acquired)
	defer release()
	_, acquired, err = q.TrySessionOperation(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.False(t, acquired)
	release()
	next, acquired, err := q.TrySessionOperation(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.True(t, acquired)
	next()
}
func TestContinueStreamDoesNotDuplicateInputOrReplayCompletedTool(t *testing.T) {
	p := &flowDoneProvider{}
	a := New(Config{Provider: p, Model: "fixture", SessionID: "s", MaxSteps: 2})
	defer a.Shutdown()
	state := &FlowState{SessionID: "s", Model: "fixture", UserInput: "original", ReadOnly: true, History: []providers.Message{{Role: "user", Content: "original"}, {Role: "assistant", Content: "already done"}}}
	stream, err := a.ContinueStream(context.Background(), state)
	require.NoError(t, err)
	for event := range stream {
		require.NoError(t, event.Error)
	}
	userMessages := 0
	for _, message := range a.History() {
		if message.Role == "user" {
			userMessages++
		}
	}
	require.Equal(t, 1, userMessages)
	require.Equal(t, security.ModeReader, a.PrivilegeMode())
}

type flowDoneProvider struct{}

func (*flowDoneProvider) Name() string              { return "fixture" }
func (*flowDoneProvider) Models() []providers.Model { return nil }
func (*flowDoneProvider) Complete(context.Context, providers.Request) (providers.Response, error) {
	return providers.Response{Content: "continued"}, nil
}
func (*flowDoneProvider) Stream(context.Context, providers.Request) (<-chan providers.Chunk, error) {
	out := make(chan providers.Chunk, 2)
	out <- providers.Chunk{Content: "continued"}
	out <- providers.Chunk{Done: true}
	close(out)
	return out, nil
}

type deadlineFlowProvider struct{ entered chan struct{} }

func (p *deadlineFlowProvider) Name() string              { return "fixture" }
func (p *deadlineFlowProvider) Models() []providers.Model { return nil }
func (p *deadlineFlowProvider) Complete(ctx context.Context, _ providers.Request) (providers.Response, error) {
	close(p.entered)
	<-ctx.Done()
	return providers.Response{}, ctx.Err()
}
func (p *deadlineFlowProvider) Stream(ctx context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
	close(p.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestDurableDeadlineAndExplicitCancelRemainDistinctRunAndStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%v/deadline=%v", stream, deadline), func(t *testing.T) {
				q, bound, state := flowQueueFixture(t)
				p := &deadlineFlowProvider{entered: make(chan struct{})}
				a := New(Config{Provider: p, Model: "fixture", SessionID: state.SessionID, Workspace: state.Workspace, MaxSteps: 2})
				defer a.Shutdown()
				a.SetFlowStore(bound, nil)
				var ctx context.Context
				var cancel context.CancelFunc
				if deadline {
					ctx, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
				} else {
					ctx, cancel = context.WithCancel(t.Context())
				}
				defer cancel()
				done := make(chan error, 1)
				go func() {
					if stream {
						events, err := a.Stream(ctx, "original task")
						if err != nil {
							done <- err
							return
						}
						for range events {
						}
						done <- ctx.Err()
					} else {
						_, err := a.Run(ctx, "original task")
						done <- err
					}
				}()
				select {
				case <-p.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("provider did not start")
				}
				if !deadline {
					cancel()
				}
				select {
				case err := <-done:
					require.Error(t, err)
				case <-time.After(3 * time.Second):
					t.Fatal("cancelled runtime did not stop")
				}
				jobs, err := q.ListSession(t.Context(), state.SessionID)
				require.NoError(t, err)
				require.Len(t, jobs, 1)
				if deadline {
					require.Equal(t, "queued", jobs[0].Status)
				} else {
					require.Equal(t, "paused", jobs[0].Status)
				}
			})
		}
	}
}
func TestNewHumanPlanCommandSupersedesPendingFlow(t *testing.T) {
	q, bound, state := flowQueueFixture(t)
	require.NoError(t, bound.Save(state))
	a := New(Config{Provider: &flowDoneProvider{}, Model: "fixture", SessionID: state.SessionID, Workspace: state.Workspace, MaxSteps: 2})
	defer a.Shutdown()
	a.SetFlowStore(bound, nil)
	events, err := a.Stream(t.Context(), "/plan")
	require.NoError(t, err)
	for range events {
	}
	jobs, err := q.ListSession(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, "paused", jobs[0].Status)
}

func TestDurableNonStreamingEstopCancelsProviderWithoutAutomaticResume(t *testing.T) {
	q, bound, state := flowQueueFixture(t)
	p := &deadlineFlowProvider{entered: make(chan struct{})}
	a := New(Config{Provider: p, Model: "fixture", SessionID: state.SessionID, Workspace: state.Workspace, MaxSteps: 2})
	defer a.Shutdown()
	a.SetFlowStore(bound, nil)
	done := make(chan error, 1)
	go func() { _, err := a.Run(t.Context(), "original task"); done <- err }()
	select {
	case <-p.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	a.EStop()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("estop did not cancel nonstreaming provider")
	}
	jobs, err := q.ListSession(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "paused", jobs[0].Status)
	saved, err := bound.Load(jobs[0].ID)
	require.NoError(t, err)
	require.Equal(t, "halted_by_estop", saved.Reason)
}

func TestDurableFlowCorruptionAndMissingStateBecomeReviewable(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			q, bound, state := flowQueueFixture(t)
			require.NoError(t, bound.Save(state))
			if missing {
				_, err := q.db.Exec(`DELETE FROM flow_states WHERE id=$1`, state.ID)
				require.NoError(t, err)
			} else {
				_, err := q.db.Exec(`UPDATE flow_states SET state='{"id":7}'::jsonb WHERE id=$1`, state.ID)
				require.NoError(t, err)
			}
			time.Sleep(3 * time.Millisecond)
			lease, _ := q.Claim(t.Context(), state.ID)
			require.Nil(t, lease)
			jobs, err := q.ListSession(t.Context(), state.SessionID)
			require.NoError(t, err)
			require.Equal(t, "manual_review", jobs[0].Status)
			due, err := q.Due(t.Context())
			require.NoError(t, err)
			require.Empty(t, due, "corruption must not create a repeated automatic retry loop")
		})
	}
}
func TestDurableFlowResumeLimitNeverPromisesAnotherAutomaticWake(t *testing.T) {
	q, bound, state := flowQueueFixture(t)
	state.Resumes = MaxResumes
	require.NoError(t, bound.Save(state))
	require.Equal(t, "exhausted", state.ResumeStatus)
	require.NotContains(t, flowCheckpointNotice(state), "will resume")
	jobs, err := q.ListSession(t.Context(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, "exhausted", jobs[0].Status)
}
func TestDurableFlowGlobalEstopFencesActiveOwnerWithoutExistingJob(t *testing.T) {
	q, bound, state := flowQueueFixture(t)
	require.NoError(t, q.PauseAll(t.Context()))
	require.ErrorContains(t, bound.Save(state), "superseded by explicit stop")
}
func TestDurableFlowPinsProviderReportedModelWithTranscript(t *testing.T) {
	q, bound, state := flowQueueFixture(t)
	state.Model = "fixture-revision-2026"
	require.NoError(t, bound.Save(state))
	saved, err := bound.Load(state.ID)
	require.NoError(t, err)
	require.Equal(t, state.Model, saved.Model)
	var model, provider string
	require.NoError(t, q.db.QueryRow(`SELECT model,provider FROM sessions WHERE id=$1`, state.SessionID).Scan(&model, &provider))
	require.Equal(t, state.Model, model)
	require.Equal(t, "fixture", provider)
}
