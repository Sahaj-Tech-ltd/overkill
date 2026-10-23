package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/require"
)

// Repeated bullets exercise the existing supported classifier/decomposer form;
// mixed numbered separators are not normalized by this regression.
const sequentialFixture = "Implement authentication fixture\n- Write regression tests for fixture\n- Document completed fixture changes"

func TestSequentialExecutionRetainsAdmissionAndOneGoalLedger(t *testing.T) {
	gs := newExecutionGoalStore(t)
	entered, release := make(chan struct{}), make(chan struct{})
	p := &goalTestProvider{complete: func(ctx context.Context, _ providers.Request) (providers.Response, error) {
		if pctx := ctx; pctx.Err() != nil {
			return providers.Response{}, pctx.Err()
		}
		select {
		case <-entered:
		default:
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return providers.Response{}, ctx.Err()
			}
		}
		time.Sleep(20 * time.Millisecond)
		return providers.Response{Content: "done", Usage: providers.Usage{InputTokens: 5, OutputTokens: 2}}, nil
	}}
	a := goalAgent(p, gs, "sequential-success")
	defer a.Shutdown()
	_, err := a.Run(t.Context(), "/think")
	require.NoError(t, err)
	require.NoError(t, gs.SetWithBudgets(t.Context(), a.SessionID(), "finish all items", 1000, 0))
	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := a.Run(t.Context(), sequentialFixture); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("sequential items never reached provider")
	}
	_, err = a.Run(t.Context(), "concurrent public call")
	require.ErrorContains(t, err, "already running")
	require.True(t, a.QueueSnapshot().Active)
	close(release)
	select {
	case err = <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("sequential execution did not finish")
	}
	elapsed := time.Since(start)
	require.EqualValues(t, 3, p.calls.Load())
	snap := a.QueueSnapshot()
	require.False(t, snap.Active)
	require.Equal(t, 3, snap.Done)
	require.Zero(t, snap.Failed)
	g := requireGoal(t, gs, a.SessionID())
	require.Equal(t, 21, g.TokensUsed)
	var rows, tokens, ledgerNS, goalNS int64
	require.NoError(t, gs.db.QueryRow(`SELECT count(*),sum(tokens_used),sum(time_used_nanoseconds) FROM goal_turn_usage WHERE session_id=$1`, a.SessionID()).Scan(&rows, &tokens, &ledgerNS))
	require.NoError(t, gs.db.QueryRow(`SELECT time_used_nanoseconds FROM goals WHERE session_id=$1`, a.SessionID()).Scan(&goalNS))
	require.EqualValues(t, 1, rows, "nested items must reuse outer goal lease")
	require.EqualValues(t, 21, tokens)
	require.Equal(t, ledgerNS, goalNS)
	require.GreaterOrEqual(t, goalNS, int64(60*time.Millisecond))
	require.LessOrEqual(t, goalNS, int64(elapsed))
}

func TestSequentialCancellationSkipsUnstartedItems(t *testing.T) {
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p := &goalTestProvider{complete: func(ctx context.Context, _ providers.Request) (providers.Response, error) {
		close(entered)
		<-ctx.Done()
		return providers.Response{}, ctx.Err()
	}}
	a := goalAgent(p, nil, "sequential-cancel")
	defer a.Shutdown()
	_, err := a.Run(ctx, "/think")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := a.Run(ctx, sequentialFixture); done <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	cancel()
	select {
	case err = <-done:
		require.True(t, errors.Is(err, context.Canceled), err)
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not stop queue")
	}
	require.EqualValues(t, 1, p.calls.Load())
	snap := a.QueueSnapshot()
	require.False(t, snap.Active)
	require.Len(t, snap.Items, 3)
	require.Equal(t, "failed", snap.Items[0].Status)
	for _, item := range snap.Items[1:] {
		require.Equal(t, "skipped", item.Status)
	}
}

func TestSequentialOneItemFallbackUsesOuterAdmission(t *testing.T) {
	p := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
		return providers.Response{Content: "fallback done"}, nil
	}}
	a := goalAgent(p, nil, "sequential-fallback")
	defer a.Shutdown()
	a.running.Store(true)
	defer a.running.Store(false)
	result, err := a.runSequential(t.Context(), "one plain task", SituationalReflection{})
	require.NoError(t, err)
	require.True(t, strings.Contains(result.Response, "fallback done"))
	require.EqualValues(t, 1, p.calls.Load())
}
