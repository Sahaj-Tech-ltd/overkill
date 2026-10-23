package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestLoadedPlanRPCDurability(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"A completed answer."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider, cfg.Agent.DefaultModel = "fixture", "gpt-4o-mini"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", BaseURL: provider.URL, APIKey: "test-only"}}
	s := NewServer(ServerConfig{Config: cfg, SessionStore: store})
	defer s.closeSessionRuntimes()
	metadata := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), metadata))
	path := filepath.Join(t.TempDir(), "Plan.md")
	require.NoError(t, os.WriteFile(path, []byte("# Explain work\n## Explain first\nFirst task\n## Explain second\nSecond task\n"), 0600))
	params, err := json.Marshal(map[string]string{"session_id": metadata.ID, "message": "/build " + path})
	require.NoError(t, err)
	_, rpcErr := s.handleAgentSend(t.Context(), params)
	require.Nil(t, rpcErr)
	require.GreaterOrEqual(t, calls.Load(), int32(2))
	callsBeforeRecovery := calls.Load()
	snapshot, err := s.loadRuntimeStatus(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.NotNil(t, snapshot.Plan)
	require.Equal(t, "completed", snapshot.Plan.Status)
	require.Equal(t, "done", snapshot.Plan.Phases[1].Status)
	cold := NewServer(ServerConfig{Config: cfg, SessionStore: store})
	defer cold.closeSessionRuntimes()
	value, rpcErr := cold.selfEvalStatus(t.Context(), mustStatusParams(t, metadata.ID))
	require.Nil(t, rpcErr)
	status := value.(map[string]any)
	require.Equal(t, "persisted", status["snapshot_origin"])
	require.Equal(t, "completed", status["plan_execution"].(*agent.PlanExecutionObservation).Status)
	snapshot.Plan.Status = "running"
	snapshot.Plan.Phases[1].Status = "running"
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, store.SaveRuntimeStatus(t.Context(), metadata.ID, raw))
	value, rpcErr = cold.selfEvalStatus(t.Context(), mustStatusParams(t, metadata.ID))
	require.Nil(t, rpcErr)
	recovered := value.(map[string]any)["plan_execution"].(*agent.PlanExecutionObservation)
	require.Equal(t, "unconfirmed", recovered.Status)
	require.Equal(t, "unconfirmed", recovered.Phases[1].Status)
	require.Equal(t, callsBeforeRecovery, calls.Load(), "cold status reads never resume provider execution")

	params, err = json.Marshal(map[string]string{"session_id": metadata.ID, "message": "Explain another fixture"})
	require.NoError(t, err)
	_, rpcErr = s.handleAgentSend(t.Context(), params)
	require.Nil(t, rpcErr)
	value, rpcErr = s.selfEvalStatus(t.Context(), mustStatusParams(t, metadata.ID))
	require.Nil(t, rpcErr)
	later := value.(map[string]any)
	require.NotEqual(t, "completed", later["status"])
	history := later["plan_execution"].(*agent.PlanExecutionObservation)
	require.Equal(t, "completed", history.Status)
	require.False(t, history.Current)
	saved, err := s.loadRuntimeStatus(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.False(t, saved.Plan.Current)
}

func TestHistoricalCompletedPlanDoesNotMaskCurrentEvaluation(t *testing.T) {
	for _, outcome := range []string{"accepted", "deferred", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			snapshot := agent.RuntimeStatusSnapshot{
				Plan:      &agent.PlanExecutionObservation{Source: "loaded_plan_executor", Status: "completed", Current: false},
				Iterative: &agent.IterativeEvaluationObservation{Status: outcome, Phase: "complete", Iteration: 1, MaxIterations: 1},
			}
			status := selfEvaluationStatus(snapshot, "live")
			require.Equal(t, outcome, status["status"])
			require.Equal(t, "self_evaluation_complete", status["phase"])
		})
	}
}
