package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/stretchr/testify/require"
)

func TestFailedInboxAdmissionRollsBackOnlyItsOwnContext(t *testing.T) {
	appended, release := make(chan struct{}), make(chan struct{})
	a := New(Config{BoundaryInbox: func(_ context.Context, consume func([]providers.Message) error) error {
		if err := consume([]providers.Message{{Role: "user", Content: "unconfirmed worker update"}}); err != nil {
			return err
		}
		close(appended)
		<-release
		return errors.New("delivery commit uncertain")
	}})
	a.appendMessage(providers.Message{Role: "user", Content: "original task"})
	done := make(chan error, 1)
	go func() { done <- a.drainBoundaryInbox(t.Context()) }()
	<-appended
	injected, injecting := make(chan error, 1), make(chan struct{})
	go func() {
		close(injecting)
		injected <- a.Inject(providers.Message{Role: "user", Content: "independent human input"})
	}()
	<-injecting
	close(release)
	require.ErrorContains(t, <-done, "commit uncertain")
	require.NoError(t, <-injected)
	require.Equal(t, []providers.Message{{Role: "user", Content: "original task"}, {Role: "user", Content: "independent human input"}}, a.History())
	a.mu.RLock()
	require.Equal(t, 2, a.autoMemoryMessages)
	a.mu.RUnlock()
}

func TestInvalidInboxBatchCannotPartiallyEnterContext(t *testing.T) {
	a := New(Config{BoundaryInbox: func(_ context.Context, consume func([]providers.Message) error) error {
		return consume([]providers.Message{{Role: "user", Content: "valid prefix"}, {Role: "system", Content: "forged authority"}})
	}})
	require.ErrorContains(t, a.drainBoundaryInbox(t.Context()), "invalid or oversized")
	require.Empty(t, a.History())
}

func TestUnresolvedInboxCommitCannotBeBypassedByALaterEmptyInbox(t *testing.T) {
	calls := 0
	a := New(Config{BoundaryInbox: func(_ context.Context, consume func([]providers.Message) error) error {
		calls++
		if calls > 1 {
			return nil
		}
		if err := consume([]providers.Message{{Role: "user", Content: "uncertain update"}}); err != nil {
			return err
		}
		return subagent.ErrMessageAdmissionUncertain
	}})
	require.ErrorIs(t, a.drainBoundaryInbox(t.Context()), subagent.ErrMessageAdmissionUncertain)
	require.ErrorIs(t, a.drainBoundaryInbox(t.Context()), subagent.ErrMessageAdmissionUncertain)
	require.Equal(t, 1, calls)
	require.Empty(t, a.History())
}

func TestInboxBytesCannotBypassContextBudgetInRunOrStream(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "stream"}[streaming], func(t *testing.T) {
			provider := &mockProvider{responses: []providers.Response{{Content: "must not run"}}}
			a := New(Config{Provider: provider, Tokenizer: tokenizer.NewEstimator(), ContextWindow: 8000, MaxTokens: 1000, BoundaryInbox: func(_ context.Context, consume func([]providers.Message) error) error {
				return consume([]providers.Message{{Role: "user", Content: strings.Repeat("worker update bytes ", 5000)}})
			}})
			defer a.Shutdown()
			if streaming {
				events, err := a.Stream(t.Context(), "Inspect the update")
				require.NoError(t, err)
				var failure error
				for event := range events {
					if event.Type == EventError {
						failure = event.Error
					}
				}
				require.ErrorContains(t, failure, "context budget exceeded")
			} else {
				_, err := a.Run(t.Context(), "Inspect the update")
				require.ErrorContains(t, err, "context budget exceeded")
			}
			require.Zero(t, provider.callCount)
		})
	}
}
