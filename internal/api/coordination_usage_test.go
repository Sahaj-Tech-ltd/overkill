package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestActualPeerTaskUsageStopsProviderAndSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits coordination.UsageLimits
		usage  string
		reason string
	}{
		{"tokens", coordination.UsageLimits{Tokens: 10}, `,"usage":{"prompt_tokens":8,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":4}}`, "task_token_limit"},
		{"cost", coordination.UsageLimits{CostUSD: .000005}, `,"usage":{"prompt_tokens":8,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":4}}`, "task_cost_limit"},
		{"unknown", coordination.UsageLimits{Tokens: 10}, ``, "task_usage_unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, _ := testpg.Open(t)
			sessions, err := session.NewPostgresStore(database)
			require.NoError(t, err)
			store, err := coordination.NewStore(t.Context(), database)
			require.NoError(t, err)
			var calls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"must-not-write","type":"function","function":{"name":"fs","arguments":"{\"action\":\"write\",\"path\":\"overflow.txt\",\"content\":\"unexpected\"}"}}]},"finish_reason":"tool_calls"}]%s}`, tc.usage)
			}))
			defer provider.Close()
			var peers []coordination.Peer
			var metadata []*session.Session
			for _, name := range []string{"frontend", "backend"} {
				peer, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "usage-fixture", Name: name, Role: name, Workspace: t.TempDir()})
				require.NoError(t, err)
				sess := session.NewSession(peer.Workspace)
				sess.Provider = "fixture"
				sess.Model = "backend"
				require.NoError(t, sessions.Create(t.Context(), sess))
				require.NoError(t, store.BindSession(t.Context(), peer.ID, sess.ID))
				peers = append(peers, peer)
				metadata = append(metadata, sess)
			}
			cfg := coordinationConfig(provider.URL)
			cfg.Providers[0].Models = []config.ModelConfig{{ID: "backend", CostIn: 1, CostOut: 2, CostCacheIn: .5}}
			server := NewServer(ServerConfig{Config: cfg, SessionStore: sessions, CoordStore: store, SessionRuntimeFactory: coordinationRuntime(t)})
			defer server.Close()
			result, rpcErr := server.handleCoordination(t.Context(), "coordination.send", coordInputForRuntime(t, map[string]any{"session_id": metadata[0].ID, "to": "backend", "kind": "request", "task_id": "budgeted-task", "payload": "do work", "idempotency_key": "task", "usage_budget": tc.limits}))
			require.Nil(t, rpcErr)
			data, err := json.Marshal(result)
			require.NoError(t, err)
			var receipt struct {
				Message coordination.Message `json:"message"`
			}
			require.NoError(t, json.Unmarshal(data, &receipt))
			require.NoError(t, server.StartBackground(t.Context()))
			require.Eventually(t, func() bool {
				m, err := store.Read(t.Context(), peers[0].ID, receipt.Message.ID)
				return err == nil && m.State == coordination.Blocked
			}, 5*time.Second, 20*time.Millisecond)
			require.EqualValues(t, 1, calls.Load())
			_, err = os.Stat(filepath.Join(metadata[1].Folder, "overflow.txt"))
			require.ErrorIs(t, err, os.ErrNotExist)
			result, rpcErr = server.handleCoordination(t.Context(), "coordination.usage", coordInputForRuntime(t, map[string]any{"session_id": metadata[0].ID, "message_id": receipt.Message.ID}))
			require.Nil(t, rpcErr)
			data, err = json.Marshal(result)
			require.NoError(t, err)
			var usage struct {
				Usage coordination.TaskUsage `json:"usage"`
			}
			require.NoError(t, json.Unmarshal(data, &usage))
			require.True(t, usage.Usage.RequiresReview)
			if tc.name != "unknown" {
				require.EqualValues(t, 10, usage.Usage.InputTokens+usage.Usage.OutputTokens)
				require.EqualValues(t, 4, usage.Usage.CachedInputTokens)
				require.InDelta(t, .000010, usage.Usage.EstimatedCostUSD, 1e-9)
			} else {
				require.EqualValues(t, 1, usage.Usage.UnknownUsage)
			}
			escalations, err := store.ReadEscalations(t.Context(), peers[0].ID, 0, 10)
			require.NoError(t, err)
			require.Len(t, escalations, 1)
			require.Equal(t, tc.reason, escalations[0].Reason)
			reopened, err := coordination.NewStore(t.Context(), database)
			require.NoError(t, err)
			durable, err := reopened.UsageForMessage(t.Context(), peers[0].ID, receipt.Message.ID)
			require.NoError(t, err)
			require.Equal(t, usage.Usage, durable)
		})
	}
}

func TestPeerCostBudgetRejectsAmbiguousOrIncompletePrices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []config.ModelConfig
		second []config.ModelConfig
	}{
		{"missing-output", []config.ModelConfig{{ID: "backend", CostIn: 1}}, nil},
		{"missing-input", []config.ModelConfig{{ID: "backend", CostOut: 2}}, nil},
		{"missing-cache", []config.ModelConfig{{ID: "backend", CostIn: 1, CostOut: 2}}, nil},
		{"conflicting-providers", []config.ModelConfig{{ID: "backend", CostIn: 1, CostOut: 2, CostCacheIn: .5}}, []config.ModelConfig{{ID: "backend", CostIn: 10, CostOut: 20, CostCacheIn: 5}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := coordinationConfig("http://fixture.invalid")
			cfg.Providers[0].Models = tc.models
			if tc.second != nil {
				cfg.Providers = append(cfg.Providers, config.ProviderConfig{Name: "second", Models: tc.second})
			}
			server := NewServer(ServerConfig{Config: cfg})
			defer server.Close()
			_, known := server.coordinationModelCost("backend", providers.Usage{InputTokens: 8, OutputTokens: 2, CachedInputTokens: 4})
			require.False(t, known)
		})
	}
}

func TestFailedUsageWriteNeverCertifiesExecutionJournal(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	_, err = database.ExecContext(t.Context(), `CREATE FUNCTION reject_usage_sample() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'controlled usage write failure'; END $$; CREATE TRIGGER fail_usage BEFORE INSERT ON coordination_usage_samples FOR EACH ROW EXECUTE FUNCTION reject_usage_sample()`)
	require.NoError(t, err)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"generated output"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`)
	}))
	defer provider.Close()
	var peers []coordination.Peer
	for _, name := range []string{"frontend", "backend"} {
		peer, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "write-failure", Name: name, Role: name, Workspace: t.TempDir()})
		require.NoError(t, err)
		sess := session.NewSession(peer.Workspace)
		sess.Provider = "fixture"
		sess.Model = "backend"
		require.NoError(t, sessions.Create(t.Context(), sess))
		require.NoError(t, store.BindSession(t.Context(), peer.ID, sess.ID))
		peers = append(peers, peer)
	}
	cfg := coordinationConfig(provider.URL)
	cfg.Providers[0].Models = []config.ModelConfig{{ID: "backend", CostIn: 1, CostOut: 2}}
	server := NewServer(ServerConfig{Config: cfg, SessionStore: sessions, CoordStore: store, SessionRuntimeFactory: coordinationRuntime(t)})
	defer server.Close()
	root, err := store.SendWithBudget(t.Context(), peers[0].ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`"work"`), IdempotencyKey: "write-failure"}, coordination.UsageLimits{Tokens: 100})
	require.NoError(t, err)
	require.NoError(t, server.StartBackground(t.Context()))
	require.Eventually(t, func() bool {
		u, err := store.UsageForMessage(t.Context(), peers[0].ID, root.ID)
		return err == nil && u.UnconfirmedExecutions == 1
	}, 5*time.Second, 20*time.Millisecond)
	message, err := store.Read(t.Context(), peers[0].ID, root.ID)
	require.NoError(t, err)
	require.Equal(t, coordination.Blocked, message.State)
	// The database recovers, but no later cleanup may erase evidence of the lost
	// sample or claim that a zero total is complete.
	_, err = database.ExecContext(t.Context(), `DROP TRIGGER fail_usage ON coordination_usage_samples`)
	require.NoError(t, err)
	reopened, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	usage, err := reopened.UsageForMessage(t.Context(), peers[0].ID, root.ID)
	require.NoError(t, err)
	require.True(t, usage.RequiresReview)
	require.EqualValues(t, 1, usage.UnconfirmedExecutions)
	require.Equal(t, "execution_usage_unknown", usage.Reason)
}
