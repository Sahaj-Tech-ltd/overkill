package coordination

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestTaskUsageDurableLimitsAndIdempotentSamples(t *testing.T) {
	store, front, back, _ := fixture(t)
	input := request("budget-root")
	input.TaskID = "shared-task"
	root, err := store.SendWithBudget(t.Context(), front.ID, input, UsageLimits{Tokens: 20, CostUSD: .01})
	require.NoError(t, err)
	again, err := store.SendWithBudget(t.Context(), front.ID, input, UsageLimits{Tokens: 20, CostUSD: .01})
	require.NoError(t, err)
	require.Equal(t, root.ID, again.ID)
	_, err = store.SendWithBudget(t.Context(), front.ID, input, UsageLimits{Tokens: 21})
	require.ErrorIs(t, err, ErrIdempotency)
	ctx, _, lease := claimLineage(t, store, back)
	defer store.ReleaseLease(t.Context(), lease)
	sample := UsageSample{Usage: providers.Usage{InputTokens: 8, OutputTokens: 2, CachedInputTokens: 4}, Reported: true, PriceKnown: true, EstimatedCostUSD: .004}
	require.NoError(t, store.RecordTaskUsage(ctx, root.ID, "one", sample))
	require.NoError(t, store.RecordTaskUsage(ctx, root.ID, "one", sample))
	changed := sample
	changed.Usage.OutputTokens++
	require.ErrorIs(t, store.RecordTaskUsage(ctx, root.ID, "one", changed), ErrIdempotency)
	restarted, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	u, err := restarted.UsageForMessage(t.Context(), front.ID, root.ID)
	require.NoError(t, err)
	require.EqualValues(t, 8, u.InputTokens)
	require.EqualValues(t, 2, u.OutputTokens)
	require.InDelta(t, .004, u.EstimatedCostUSD, 1e-9)
	require.ErrorIs(t, restarted.RecordTaskUsage(ctx, root.ID, "two", sample), ErrUsageBudget)
	require.ErrorIs(t, restarted.CheckTaskUsage(ctx, root.ID), ErrUsageBudget)
	events, err := store.ReadEscalations(t.Context(), front.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "task_token_limit", events[0].Reason)
	require.ErrorIs(t, store.RecordTaskUsage(context.Background(), root.ID, "forged", sample), ErrUnauthorized)
}

func TestTaskUsageSharedAcrossPeerExecutionsAndConversations(t *testing.T) {
	store, front, back, _ := fixture(t)
	third, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: "qa", Role: "test", Workspace: t.TempDir()})
	require.NoError(t, err)
	input := request("root")
	input.TaskID = "one-task"
	root, err := store.SendWithBudget(t.Context(), front.ID, input, UsageLimits{Tokens: 15})
	require.NoError(t, err)
	backCtx, _, backLease := claimLineage(t, store, back)
	defer store.ReleaseLease(t.Context(), backLease)
	child, err := store.Send(backCtx, back.ID, SendInput{To: third.Name, TaskID: "forged-reset", Kind: "request", Payload: json.RawMessage(`{}`), IdempotencyKey: "child"})
	require.NoError(t, err)
	require.Equal(t, input.TaskID, child.TaskID)
	thirdCtx, _, thirdLease := claimLineage(t, store, third)
	defer store.ReleaseLease(t.Context(), thirdLease)
	sample := UsageSample{Usage: providers.Usage{InputTokens: 5, OutputTokens: 3}, Reported: true, PriceKnown: true}
	require.NoError(t, store.RecordTaskUsage(backCtx, root.ID, "backend", sample))
	require.ErrorIs(t, store.RecordTaskUsage(thirdCtx, child.ID, "qa", sample), ErrUsageBudget)
	u, err := store.UsageForMessage(t.Context(), third.ID, child.ID)
	require.NoError(t, err)
	require.EqualValues(t, 16, u.InputTokens+u.OutputTokens)
	extra := request("new-conversation")
	extra.TaskID = input.TaskID
	extra.To = third.Name
	_, err = store.Send(t.Context(), back.ID, extra)
	require.ErrorIs(t, err, ErrUnauthorized, "another peer cannot start a root against an owned task")
	other, err := store.Send(t.Context(), front.ID, extra)
	require.NoError(t, err)
	u2, err := store.UsageForMessage(t.Context(), front.ID, other.ID)
	require.NoError(t, err)
	require.Equal(t, u.Scope, u2.Scope)
	unrelated, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: "unrelated", Role: "observer", Workspace: t.TempDir()})
	require.NoError(t, err)
	_, err = store.UsageForMessage(t.Context(), unrelated.ID, root.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestTaskUsageCostUnknownAndConcurrentAccounting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits UsageLimits
		sample UsageSample
		reason string
	}{
		{"cost", UsageLimits{CostUSD: .001}, UsageSample{Usage: providers.Usage{InputTokens: 10}, Reported: true, PriceKnown: true, EstimatedCostUSD: .002}, "task_cost_limit"},
		{"usage", UsageLimits{Tokens: 10}, UsageSample{PriceKnown: true}, "task_usage_unknown"},
		{"price", UsageLimits{CostUSD: 1}, UsageSample{Usage: providers.Usage{InputTokens: 1}, Reported: true}, "task_cost_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, front, back, _ := fixture(t)
			m, err := store.SendWithBudget(t.Context(), front.ID, request("root"), tc.limits)
			require.NoError(t, err)
			ctx, _, lease := claimLineage(t, store, back)
			defer store.ReleaseLease(t.Context(), lease)
			require.ErrorIs(t, store.RecordTaskUsage(ctx, m.ID, "sample", tc.sample), ErrUsageBudget)
			events, err := store.ReadEscalations(t.Context(), front.ID, 0, 10)
			require.NoError(t, err)
			require.Equal(t, tc.reason, events[0].Reason)
		})
	}
	store, front, back, _ := fixture(t)
	m, err := store.Send(t.Context(), front.ID, request("concurrent"))
	require.NoError(t, err)
	ctx, _, lease := claimLineage(t, store, back)
	defer store.ReleaseLease(t.Context(), lease)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			require.NoError(t, store.RecordTaskUsage(ctx, m.ID, fmt.Sprint(i), UsageSample{Usage: providers.Usage{InputTokens: 2, OutputTokens: 1}, Reported: true, PriceKnown: true, EstimatedCostUSD: .000000001}))
		}(i)
	}
	wg.Wait()
	u, err := store.UsageForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.EqualValues(t, 60, u.InputTokens+u.OutputTokens)
	require.InDelta(t, 20e-9, u.EstimatedCostUSD, 1e-12)
	for _, limits := range []UsageLimits{{Tokens: -1}, {CostUSD: math.NaN()}, {CostUSD: math.Inf(1)}} {
		_, err := store.SendWithBudget(t.Context(), front.ID, request("invalid"), limits)
		require.Error(t, err)
	}
}

func TestTaskUsageJournalExposesLostExecutionBeforeFurtherSpending(t *testing.T) {
	store, front, back, _ := fixture(t)
	input := request("lost-worker")
	input.TaskID = "journaled-task"
	root, err := store.SendWithBudget(t.Context(), front.ID, input, UsageLimits{Tokens: 100})
	require.NoError(t, err)
	ctx, _, lease := claimLineage(t, store, back)
	require.NoError(t, store.BeginTaskUsage(ctx, root.ID))
	live, err := store.UsageForMessage(t.Context(), front.ID, root.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, live.PendingExecutions)
	require.Zero(t, live.UnconfirmedExecutions)
	require.NoError(t, store.ReleaseLease(t.Context(), lease))
	late := UsageSample{Usage: providers.Usage{InputTokens: 2}, Reported: true, PriceKnown: true}
	require.ErrorIs(t, store.RecordTaskUsage(ctx, root.ID, "late", late), ErrLeaseLost)
	lost, err := store.UsageForMessage(t.Context(), front.ID, root.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, lost.UnconfirmedExecutions)
	require.True(t, lost.RequiresReview)
	// Another peer doing legitimate child work cannot spend past an unresolved
	// provider call from a vanished worker in the same task.
	_, err = store.WorkerTransition(t.Context(), lease, root.ID, root.Revision, "blocked", json.RawMessage(`{}`))
	require.Error(t, err)
	third, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: "other", Role: "worker", Workspace: t.TempDir()})
	require.NoError(t, err)
	extra := request("other-peer")
	extra.TaskID = input.TaskID
	extra.To = third.Name
	m, err := store.Send(t.Context(), front.ID, extra)
	require.NoError(t, err)
	otherCtx, _, otherLease := claimLineage(t, store, third)
	defer store.ReleaseLease(t.Context(), otherLease)
	require.ErrorIs(t, store.CheckTaskUsage(otherCtx, m.ID), ErrUsageBudget)
	events, err := store.ReadEscalations(t.Context(), front.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "task_execution_usage_unknown", events[0].Reason)
	reopened, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	after, err := reopened.UsageForMessage(t.Context(), front.ID, root.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, after.UnconfirmedExecutions)
}

func TestUnnamedConversationUsageHasRootOwnership(t *testing.T) {
	store, front, back, _ := fixture(t)
	root, err := store.SendWithBudget(t.Context(), front.ID, request("unnamed"), UsageLimits{Tokens: 1})
	require.NoError(t, err)
	duplicate := request("other-root")
	duplicate.To = front.Name
	duplicate.ConversationID = root.ConversationID
	_, err = store.Send(t.Context(), back.ID, duplicate)
	require.ErrorIs(t, err, ErrUnauthorized)
	duplicate.To = back.Name
	retry, err := store.SendWithBudget(t.Context(), front.ID, duplicate, UsageLimits{Tokens: 1})
	require.NoError(t, err)
	require.Equal(t, root.ConversationID, retry.ConversationID)
	u, err := store.UsageForMessage(t.Context(), front.ID, retry.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, u.Limits.Tokens)
}
