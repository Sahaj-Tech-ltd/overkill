package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

type freshFollowupContext struct{}

func TestWorkerFollowupUsesFreshContextAndIntersectionOfCurrentToolGrant(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := subagent.NewCommunicationStore(t.Context(), db, "parent-chat")
	require.NoError(t, err)
	require.NoError(t, store.RegisterBatch(t.Context(), []subagent.WorkerRecord{{ID: "old-worker", Name: "backend", Goal: "Original task with a prior external effect", Tools: []string{"fs", "shell", "terminal"}}}))
	require.NoError(t, store.Finish(t.Context(), "old-worker", &subagent.Result{Status: "completed", Summary: "Prior evidence; must be revalidated"}))
	manager := subagent.NewManager(subagent.Config{MaxChildren: 1, MaxDepth: 2, DefaultTools: []string{"fs"}})
	manager.SetCommunicationStore(store)
	var spawned int
	manager.SetWorkerFactory(func(cfg subagent.RealWorkerConfig) (subagent.WorkerRunner, error) {
		spawned++
		require.Equal(t, []string{"fs"}, cfg.Tools)
		require.NotContains(t, cfg.Goal, "prior external effect")
		require.Contains(t, cfg.Goal, "Explicit follow-up")
		require.Contains(t, cfg.Context, "Previous task (context only")
		return delegatedGuardRunner{run: func(ctx context.Context) (*subagent.Result, error) {
			require.Equal(t, "fresh-budget-admission", ctx.Value(freshFollowupContext{}))
			return &subagent.Result{Status: "completed", Summary: "Fresh attempt completed"}, nil
		}}, nil
	})
	ctx := context.WithValue(t.Context(), freshFollowupContext{}, "fresh-budget-admission")
	output, err := NewDelegateTool(manager).Execute(ctx, json.RawMessage(`{"followup_of":"old-worker","goal":"Inspect current files and make the requested small change"}`))
	require.NoError(t, err)
	var result subagent.Result
	require.NoError(t, json.Unmarshal(output, &result))
	require.NotEqual(t, "old-worker", result.WorkerID)
	require.Equal(t, 1, spawned)
	fresh, err := store.ReadWorker(t.Context(), result.WorkerID)
	require.NoError(t, err)
	require.Equal(t, "old-worker", fresh.FollowupOf)
	require.Equal(t, []string{"fs"}, fresh.Tools)
	require.NoError(t, store.RegisterBatch(t.Context(), []subagent.WorkerRecord{{ID: "cancelled-worker", Name: "cancelled", Goal: "Interrupted task", Tools: []string{"shell"}}}))
	require.NoError(t, store.Finish(t.Context(), "cancelled-worker", &subagent.Result{Status: "interrupted"}))
	_, err = NewDelegateTool(manager).Execute(ctx, json.RawMessage(`{"followup_of":"cancelled-worker","goal":"Continue"}`))
	require.ErrorContains(t, err, "requires review")
	require.Equal(t, 1, spawned)
}
