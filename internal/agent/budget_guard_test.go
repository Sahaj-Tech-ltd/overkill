package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/stretchr/testify/require"
)

type preservingCompactor struct {
	called bool
	err    error
}

func (c *preservingCompactor) Compact(_ context.Context, history []providers.Message, _ string, capacity int) ([]providers.Message, string, error) {
	c.called = true
	if c.err != nil {
		return nil, "", c.err
	}
	return []providers.Message{{Role: "assistant", Content: "summary"}, history[len(history)-1]}, "summary", nil
}

func TestBudgetGuardCompactsBeforeProviderAndPreservesRequest(t *testing.T) {
	provider := &mockProvider{responses: []providers.Response{{Content: "done"}}}
	a := New(Config{Provider: provider, Tokenizer: tokenizer.NewEstimator(), ContextWindow: 8000, MaxTokens: 1000})
	defer a.Shutdown()
	a.RestoreHistory([]providers.Message{{Role: "assistant", Content: strings.Repeat("long history ", 6000)}})
	compactor := &preservingCompactor{}
	a.SetCompactor(compactor, true)
	_, err := a.Run(t.Context(), "keep the current request")
	require.NoError(t, err)
	require.True(t, compactor.called)
	require.Equal(t, 1, provider.callCount)
	history := a.History()
	require.Equal(t, "summary", history[0].Content)
	require.Equal(t, "keep the current request", history[1].Content)
}

func TestBudgetGuardRejectsOverflowWhenCompactionFails(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "stream"}[streaming], func(t *testing.T) {
			provider := &mockProvider{responses: []providers.Response{{Content: "must not run"}}}
			a := New(Config{Provider: provider, Tokenizer: tokenizer.NewEstimator(), ContextWindow: 8000})
			defer a.Shutdown()
			a.RestoreHistory([]providers.Message{{Role: "assistant", Content: strings.Repeat("long history ", 6000)}})
			a.SetCompactor(&preservingCompactor{err: errors.New("summary unavailable")}, true)
			if streaming {
				events, err := a.Stream(t.Context(), "continue")
				require.NoError(t, err)
				var failure error
				for event := range events {
					require.NotEqual(t, EventDone, event.Type)
					if event.Type == EventError {
						failure = event.Error
					}
				}
				require.ErrorContains(t, failure, "context budget exceeded")
			} else {
				_, err := a.Run(t.Context(), "continue")
				require.ErrorContains(t, err, "context budget exceeded")
			}
			require.Zero(t, provider.callCount)
		})
	}
}

func TestStreamRejectsOverlappingTurns(t *testing.T) {
	a := New(Config{Provider: &mockProvider{}})
	defer a.Shutdown()
	a.running.Store(true)
	_, err := a.Stream(t.Context(), "concurrent")
	require.ErrorContains(t, err, "already running")
	require.Empty(t, a.History())
	a.running.Store(false)
}
