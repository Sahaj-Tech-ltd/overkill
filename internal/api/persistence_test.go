package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestAPIConversationSurvivesAgentRecreation(t *testing.T) {
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "test-provider"
	cfg.Agent.DefaultModel = "test-model"
	cfg.Providers = []config.ProviderConfig{{Name: "test-provider", Type: "openai", APIKey: "test-only", Models: []config.ModelConfig{{ID: "test-model", MaxTokens: 12000}}}}
	s := &Server{cfg: cfg, sessionStore: store, toolRegistry: tools.NewRegistry()}
	ctx := context.Background()
	sess := session.NewSession(t.TempDir())
	sess.Title = "persistent conversation"
	require.NoError(t, store.Create(ctx, sess))
	first, err := s.createAgent(ctx, sess.ID)
	require.NoError(t, err)
	defer first.Shutdown()
	require.Equal(t, 12000, first.BudgetReport().MaxTokens)
	history := []providers.Message{{Role: "user", Content: "remember the API contract"}, {Role: "assistant", Content: "GET /users returns a list"}}
	first.RestoreHistory(history)
	require.NoError(t, s.saveSessionState(ctx, sess.ID, first))
	// A fresh agent represents a server restart or evicted session cache.
	second, err := s.createAgent(ctx, sess.ID)
	require.NoError(t, err)
	defer second.Shutdown()
	require.Equal(t, history, second.History())
	_, err = s.createAgent(ctx, "missing")
	require.ErrorContains(t, err, "load session")
}

func TestStreamCompletionWaitsForPersistence(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "failed"}[fail], func(t *testing.T) {
			events := make(chan agent.StreamEvent, 2)
			events <- agent.StreamEvent{Type: agent.EventToken, Content: "answer"}
			events <- agent.StreamEvent{Type: agent.EventDone}
			close(events)
			recorder := httptest.NewRecorder()
			called := false
			closed := (&Server{}).consumeStreamEvents(t.Context(), recorder, recorder, events, func() error {
				called = true
				require.NotContains(t, recorder.Body.String(), "event: done")
				if fail {
					return errors.New("database unavailable")
				}
				return nil
			})
			require.True(t, closed)
			require.True(t, called)
			require.Contains(t, recorder.Body.String(), "answer")
			if fail {
				require.NotContains(t, recorder.Body.String(), "event: done")
				require.Contains(t, recorder.Body.String(), "database unavailable")
			} else {
				require.Contains(t, recorder.Body.String(), "event: done")
			}
		})
	}
}

func TestModelTokenLimitsSeparateContextFromOutput(t *testing.T) {
	window, output, err := providers.ResolveTokenLimits([]providers.Model{{ID: "m", MaxTokens: 12000, DefaultMaxTokens: 1000}}, "m", 0)
	require.NoError(t, err)
	require.Equal(t, 12000, window)
	require.Equal(t, 1000, output)
	window, output, err = providers.ResolveTokenLimits([]providers.Model{{ID: "m", ContextWindow: 16000, MaxTokens: 12000, DefaultMaxTokens: 16000}}, "m", 0)
	require.NoError(t, err)
	require.Equal(t, 16000, window)
	require.Zero(t, output, "context capacity must not become output limit")
	window, output, err = providers.ResolveTokenLimits(nil, "custom", 0)
	require.NoError(t, err)
	require.Positive(t, window)
	require.Zero(t, output)
}
