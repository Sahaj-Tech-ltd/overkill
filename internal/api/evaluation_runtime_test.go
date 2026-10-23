package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestIterativeEvaluationAPIOptInAndDurability(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"A completed answer."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Security.AutonomyLevel = "auto"
	cfg.Agent.DefaultProvider, cfg.Agent.DefaultModel = "fixture", "gpt-4o-mini"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", BaseURL: provider.URL, APIKey: "test-only"}}
	s := NewServer(ServerConfig{Config: cfg, SessionStore: store})
	defer s.closeSessionRuntimes()
	metadata := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), metadata))
	untouched := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), untouched))
	// Admission through the production RPC builds the configured agent.
	params, err := json.Marshal(map[string]string{"session_id": metadata.ID, "message": "Explain this fixture"})
	require.NoError(t, err)
	_, rpcErr := s.handleAgentSend(t.Context(), params)
	require.Nil(t, rpcErr)
	live, rpcErr := s.selfEvalStatus(t.Context(), mustStatusParams(t, metadata.ID))
	require.Nil(t, rpcErr)
	liveStatus := live.(map[string]any)
	require.Equal(t, true, liveStatus["self_evaluation_available"])
	observation := liveStatus["iterative_evaluation"].(*agent.IterativeEvaluationObservation)
	require.Contains(t, []string{"accepted", "deferred"}, observation.Status)
	require.False(t, liveStatus["active"].(bool))
	require.Nil(t, liveStatus["red_team_passed"])
	require.GreaterOrEqual(t, observation.Iteration, 1)
	// A second server owns no live agent; it reads the same per-session durable observation.
	cold := NewServer(ServerConfig{Config: cfg, SessionStore: store})
	defer cold.closeSessionRuntimes()
	restored, rpcErr := cold.selfEvalStatus(t.Context(), mustStatusParams(t, metadata.ID))
	require.Nil(t, rpcErr)
	restoredStatus := restored.(map[string]any)
	require.Equal(t, "persisted", restoredStatus["snapshot_origin"])
	require.Equal(t, observation.Status, restoredStatus["status"])
	require.Equal(t, observation.Iteration, restoredStatus["iteration"])
	empty, rpcErr := cold.selfEvalStatus(t.Context(), mustStatusParams(t, untouched.ID))
	require.Nil(t, rpcErr)
	require.Equal(t, false, empty.(map[string]any)["self_evaluation_available"])
	// Persisted in-flight loop work never claims a live evaluation after recovery.
	snapshot, err := s.loadRuntimeStatus(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	snapshot.Iterative.Status, snapshot.Iterative.Phase = "running", "assessing"
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, store.SaveRuntimeStatus(t.Context(), metadata.ID, raw))
	recovered, rpcErr := cold.selfEvalStatus(t.Context(), mustStatusParams(t, metadata.ID))
	require.Nil(t, rpcErr)
	require.Equal(t, "unconfirmed", recovered.(map[string]any)["status"])
	require.Equal(t, false, recovered.(map[string]any)["active"])
}

func mustStatusParams(t *testing.T, id string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]string{"session_id": id})
	require.NoError(t, err)
	return data
}

func TestIterativeEvaluationCompletedHistoryDoesNotHideCurrentWork(t *testing.T) {
	for _, current := range []string{"verification", "confidence", "iterative"} {
		t.Run(current, func(t *testing.T) {
			snapshot := agent.RuntimeStatusSnapshot{
				SessionID:    "active-session",
				Verification: agent.VerificationStatus{Source: "post_write_verifier", Tests: []agent.VerificationCheck{}},
				Iterative:    &agent.IterativeEvaluationObservation{Source: "iterative_self_evaluation", Status: "accepted", Phase: "complete", Iteration: 2, MaxIterations: 6},
			}
			expected := "verifying"
			switch current {
			case "verification":
				snapshot.Verification.Running = true
			case "confidence":
				snapshot.Evaluation = &agent.ConfidenceObservation{Status: "running"}
				expected = "assessing_confidence"
			case "iterative":
				snapshot.Verification.Running = true
				snapshot.Iterative.Status, snapshot.Iterative.Phase = "running", "red_team"
				expected = "self_evaluation_red_team"
			}
			status := selfEvaluationStatus(snapshot, "live")
			require.Equal(t, "running", status["status"])
			require.Equal(t, expected, status["phase"])
			require.Equal(t, true, status["active"])
			require.Equal(t, 2, status["iteration"], "historical iteration metadata remains available separately")
			require.Equal(t, 6, status["max_iterations"])
		})
	}
}
