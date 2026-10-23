package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/modelusage"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestParentTaskAccountingCoexistsWithGoalAndLateCacheUsage(t *testing.T) {
	gs := newExecutionGoalStore(t)
	a := goalAgent(&goalTestProvider{stream: goalChunks(
		providers.Chunk{Content: "answer", Usage: &providers.Usage{InputTokens: 10}},
		providers.Chunk{Usage: &providers.Usage{InputTokens: 10, OutputTokens: 4, CachedInputTokens: 6}},
		providers.Chunk{Usage: &providers.Usage{InputTokens: 10, OutputTokens: 4, CachedInputTokens: 6}, Done: true},
	)}, gs, "task-stream")
	require.NoError(t, a.SetGoal(t.Context(), "complete within task"))
	var mu sync.Mutex
	var samples []providers.Usage
	var models []string
	ctx := modelusage.WithAccounting(t.Context(), modelusage.Accounting{Record: func(_ context.Context, model string, usage providers.Usage, reported bool) error {
		require.True(t, reported)
		mu.Lock()
		defer mu.Unlock()
		samples = append(samples, usage)
		models = append(models, model)
		return nil
	}})
	_, err := goalStream(ctx, a)
	require.NoError(t, err)
	mu.Lock()
	require.Equal(t, []providers.Usage{{InputTokens: 10, OutputTokens: 4, CachedInputTokens: 6}}, samples)
	require.Equal(t, []string{"goal-test"}, models)
	mu.Unlock()
	require.Equal(t, 14, requireGoal(t, gs, "task-stream").TokensUsed)
}

func TestParentTaskMissingUsageAndFailureStopBeforeTools(t *testing.T) {
	limited := errors.New("task usage requires review")
	provider := &goalTestProvider{stream: goalChunks(providers.Chunk{Content: "answer", Done: true})}
	a := goalAgent(provider, nil, "unknown-stream")
	var unknown bool
	ctx := modelusage.WithAccounting(t.Context(), modelusage.Accounting{Record: func(_ context.Context, model string, usage providers.Usage, reported bool) error {
		unknown = !reported
		return limited
	}})
	result, err := goalStream(ctx, a)
	require.ErrorIs(t, err, limited)
	require.Nil(t, result)
	require.True(t, unknown)
	require.EqualValues(t, 1, provider.calls.Load())
}

func TestParentTaskCancellationRetainsLastReportedStreamUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var mu sync.Mutex
	var got providers.Usage
	provider := &goalTestProvider{stream: func(ctx context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
		ch := make(chan providers.Chunk)
		go func() {
			defer close(ch)
			select {
			case ch <- providers.Chunk{Content: "partial", Usage: &providers.Usage{InputTokens: 7, OutputTokens: 2}}:
			case <-ctx.Done():
				return
			}
			<-ctx.Done()
		}()
		return ch, nil
	}}
	a := goalAgent(provider, nil, "cancel-stream")
	ctx = modelusage.WithAccounting(ctx, modelusage.Accounting{Record: func(_ context.Context, _ string, u providers.Usage, reported bool) error {
		require.False(t, reported)
		mu.Lock()
		got = u
		mu.Unlock()
		return nil
	}})
	events, err := a.Stream(ctx, "work")
	require.NoError(t, err)
	for event := range events {
		if event.Type == EventToken {
			cancel()
		}
	}
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, providers.Usage{InputTokens: 7, OutputTokens: 2}, got)
}

func TestParentTaskStreamStartFailureIsUnknown(t *testing.T) {
	failed := errors.New("upstream start failed")
	provider := &goalTestProvider{stream: func(context.Context, providers.Request) (<-chan providers.Chunk, error) { return nil, failed }}
	a := goalAgent(provider, nil, "failed-stream")
	calls := 0
	ctx := modelusage.WithAccounting(t.Context(), modelusage.Accounting{Record: func(_ context.Context, _ string, u providers.Usage, reported bool) error {
		calls++
		require.False(t, reported)
		require.Equal(t, providers.Usage{}, u)
		return nil
	}})
	_, err := goalStream(ctx, a)
	require.ErrorIs(t, err, failed)
	require.Equal(t, 1, calls)
}
