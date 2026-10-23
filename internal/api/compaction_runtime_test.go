package api

import (
	"context"
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"testing"
)

func TestStandaloneAPICompactorClonesSessionMemoryRegistries(t *testing.T) {
	db, _ := testpg.Open(t)
	cfg := config.Default()
	cfg.Compaction.PreserveMessages = 1
	cfg.Compaction.MaxSummaryTokens = 128
	shared := tools.NewRegistry()
	server := NewServer(ServerConfig{Config: cfg, DB: db, Tools: shared})
	defer server.closeSessionRuntimes()
	provider := providers.NewMockProvider("fixture", []providers.Model{{ID: "summary", ContextWindow: 8192}}, func(providers.Request) (providers.Response, error) {
		return providers.Response{Content: "Observed state retained."}, nil
	})
	c, use, first, err := server.sessionHistoryCompactor(context.Background(), "first", provider, &SessionRuntime{}, shared)
	if err != nil || !use {
		t.Fatalf("first bound factory: %v", err)
	}
	_, use, second, err := server.sessionHistoryCompactor(context.Background(), "second", provider, &SessionRuntime{}, shared)
	if err != nil || !use {
		t.Fatalf("second bound factory: %v", err)
	}
	if first == second || first == shared || shared.Has("memory_search") {
		t.Fatal("session-bound tools entered shared registry")
	}
	_, _, err = c.Compact(context.Background(), []providers.Message{{Role: "user", Content: "distinctive wombat evidence"}, {Role: "assistant", Content: "observed"}, {Role: "user", Content: "current"}}, "summary", 8192)
	if err != nil {
		t.Fatal(err)
	}
	search, err := first.Get("memory_search")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := search.Execute(context.Background(), json.RawMessage(`{"query":"wombat"}`))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Sources []struct {
			SourceID string `json:"source_id"`
		} `json:"sources"`
	}
	if err = json.Unmarshal(raw, &result); err != nil || len(result.Sources) != 1 {
		t.Fatalf("bound search: %s %v", raw, err)
	}
	expand, err := second.Get("memory_expand")
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]string{"source_id": result.Sources[0].SourceID})
	if _, err = expand.Execute(context.Background(), input); err == nil {
		t.Fatal("standalone API leaked raw source across sessions")
	}
}
