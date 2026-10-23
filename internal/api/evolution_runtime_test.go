package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestEvolutionActualHTTPRecordsGeneratesReviewsReloadsAndRollsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OVERKILL_HOME", home)
	db, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	tracker, err := cost.NewPostgresTracker(db, config.CostConfig{})
	require.NoError(t, err)
	const guidance = "# Workspace checklist\nRun project checks and report observed results. EVOLVED_PROJECT_MARKER"
	var mu sync.Mutex
	var prompts []string
	generated := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string }
			Stream   bool
			Tools    []any
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, "Bearer fake-evolution-private-key", r.Header.Get("Authorization"))
		system := ""
		for _, m := range request.Messages {
			if m.Role == "system" {
				system += m.Content
			}
		}
		content := "Completed project observation."
		mu.Lock()
		if strings.HasPrefix(system, "Propose one reviewable project skill") {
			generated++
			require.Empty(t, request.Tools)
			raw, _ := json.Marshal(map[string]any{"targetSkillName": "project-checklist", "draftType": "workflow", "changeKind": "create", "bodyOrPatch": guidance})
			content = string(raw)
		} else {
			prompts = append(prompts, system)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 5}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 5}}))
	}))
	defer fixture.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "gpt-4o"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fake-evolution-private-key", BaseURL: fixture.URL}}
	first := session.NewSession(t.TempDir())
	first.Provider = "fixture"
	first.Model = "gpt-4o"
	require.NoError(t, store.Create(t.Context(), first))
	other := session.NewSession(t.TempDir())
	other.Provider = "fixture"
	other.Model = first.Model
	require.NoError(t, store.Create(t.Context(), other))
	var server *Server
	var endpoint *httptest.Server
	start := func() {
		server = NewServer(ServerConfig{Config: cfg, SessionStore: store, CostTracker: tracker})
		mux := http.NewServeMux()
		mux.HandleFunc("/rpc", server.withMiddleware(server.handleRPC))
		mux.HandleFunc("/stream", server.withMiddleware(server.handleStream))
		endpoint = httptest.NewServer(mux)
	}
	start()
	defer func() { endpoint.Close(); server.Close() }()
	rpc := func(method, id, draft string, approve *bool) (json.RawMessage, *RPCError) {
		params := map[string]any{"session_id": id}
		if draft != "" {
			params["id"] = draft
		}
		if approve != nil {
			params["approve"] = *approve
		}
		if method == "agent.send" {
			params["message"] = "Observe project behavior."
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "fixture", "method": method, "params": params})
		req, err := http.NewRequestWithContext(t.Context(), "POST", endpoint.URL+"/rpc", bytes.NewReader(raw))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+server.AuthToken())
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, 200, response.StatusCode)
		var result struct {
			Result json.RawMessage `json:"result"`
			Error  *RPCError       `json:"error"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
		return result.Result, result.Error
	}
	for i := 0; i < 2; i++ {
		_, rpcErr := rpc("agent.send", first.ID, "", nil)
		require.Nil(t, rpcErr)
	}
	req, err := http.NewRequestWithContext(t.Context(), "GET", endpoint.URL+"/stream?session_id="+first.ID+"&message=Observe%20streamed%20project%20behavior.", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+server.AuthToken())
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	raw, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Contains(t, string(raw), "done")
	canonical, err := filepath.EvalSymlinks(first.Folder)
	require.NoError(t, err)
	spool, err := os.ReadFile(filepath.Join(home, "learning", "workspaces", learning.WorkspaceScope(canonical), "records", "turns.jsonl"))
	require.NoError(t, err)
	require.Equal(t, 3, strings.Count(string(spool), "\n"))
	require.NotContains(t, string(spool), "fake-evolution-private-key")
	require.NotContains(t, string(spool), "Observe project behavior.")
	result, rpcErr := rpc("evolution.generate", first.ID, "", nil)
	require.Nil(t, rpcErr)
	var generatedResult learning.ColdPathResult
	require.NoError(t, json.Unmarshal(result, &generatedResult))
	require.Equal(t, 3, generatedResult.RecordsLoaded)
	require.Equal(t, 1, generatedResult.CandidateDrafts)
	id := generatedResult.Drafts[0].ID
	_, rpcErr = rpc("evolution.apply", first.ID, id, nil)
	require.NotNil(t, rpcErr, "candidate cannot install itself")
	_, rpcErr = rpc("agent.send", first.ID, "", nil)
	require.Nil(t, rpcErr)
	mu.Lock()
	require.NotContains(t, prompts[len(prompts)-1], guidance)
	require.Equal(t, 1, generated)
	mu.Unlock()
	yes := true
	_, rpcErr = rpc("evolution.review", other.ID, id, &yes)
	require.NotNil(t, rpcErr, "cross-project review must fail")
	_, rpcErr = rpc("evolution.review", first.ID, id, &yes)
	require.Nil(t, rpcErr)
	_, rpcErr = rpc("evolution.apply", first.ID, id, nil)
	require.Nil(t, rpcErr)
	endpoint.Close()
	server.Close()
	start()
	_, rpcErr = rpc("agent.send", first.ID, "", nil)
	require.Nil(t, rpcErr)
	mu.Lock()
	require.Contains(t, prompts[len(prompts)-1], guidance)
	mu.Unlock()
	_, rpcErr = rpc("agent.send", other.ID, "", nil)
	require.Nil(t, rpcErr)
	mu.Lock()
	require.NotContains(t, prompts[len(prompts)-1], guidance)
	mu.Unlock()
	_, rpcErr = rpc("evolution.rollback", first.ID, id, nil)
	require.Nil(t, rpcErr)
	_, rpcErr = rpc("agent.send", first.ID, "", nil)
	require.Nil(t, rpcErr)
	mu.Lock()
	require.NotContains(t, prompts[len(prompts)-1], guidance)
	mu.Unlock()
	var entries, tokens int
	require.NoError(t, db.QueryRow(`SELECT count(*),sum(input_tokens+output_tokens) FROM cost_records WHERE task_id='skill-evolution' AND session_id=$1`, first.ID).Scan(&entries, &tokens))
	require.Equal(t, 1, entries)
	require.Equal(t, 16, tokens)
	var dollars float64
	require.NoError(t, db.QueryRow(`SELECT sum(cost_usd) FROM cost_records WHERE task_id='skill-evolution' AND session_id=$1`, first.ID).Scan(&dollars))
	require.Positive(t, dollars, "known-model generation must record dollars as well as tokens")
	anonymous, err := http.Post(endpoint.URL+"/rpc", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"evolution.list","params":{}}`))
	require.NoError(t, err)
	anonymous.Body.Close()
	require.Equal(t, 401, anonymous.StatusCode)
}

func TestEvolutionDollarCapRejectsUnknownModelPricing(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	db, _ := testpg.Open(t)
	cfg := config.Default()
	cfg.Cost.DailyLimitUSD = 1
	cfg.Agent.DefaultModel = "unpriced-fixture"
	tracker, err := cost.NewPostgresTracker(db, cfg.Cost)
	require.NoError(t, err)
	metadata := session.NewSession(workspace)
	metadata.Model = "unpriced-fixture"
	provider := providers.NewMockProvider("fixture", []providers.Model{{ID: metadata.Model}}, func(providers.Request) (providers.Response, error) {
		t.Error("unpriced model must not be called under a dollar cap")
		return providers.Response{}, nil
	})
	engine, err := NewSessionEvolution(cfg, home, metadata, provider, tracker)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		require.NoError(t, engine.RecordTurn(t.Context(), learning.LearningRecord{FinalOutput: "Observed project behavior."}))
	}
	result, err := engine.RunColdPath(t.Context(), "", false)
	require.NoError(t, err)
	require.Equal(t, 1, result.GenerationErrors)
	require.Empty(t, result.Drafts)
}
