package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type Orchestrator struct {
	store    Store
	provider providers.Provider
	model    string

	// Optional embedding/vector backend. Wired via AttachEmbeddings.
	embed  EmbeddingClient
	semCfg SemanticConfig
}

func NewOrchestrator(store Store, provider providers.Provider, model string) *Orchestrator {
	return &Orchestrator{
		store:    store,
		provider: provider,
		model:    model,
	}
}

func (o *Orchestrator) Remember(ctx context.Context, content string, memoryType MemoryType, tags []string, sessionID string) error {
	m := &Memory{
		Type:      memoryType,
		Content:   content,
		Tags:      tags,
		SessionID: sessionID,
		Timestamp: time.Now().UTC(),
		Metadata:  make(map[string]string),
	}

	if err := o.store.Store(ctx, m); err != nil {
		return fmt.Errorf("memory: remember: %w", err)
	}

	return nil
}

func (o *Orchestrator) Recall(ctx context.Context, query string, limit int) (*SearchResult, error) {
	q := Query{
		Content: query,
		Limit:   limit,
	}

	result, err := o.store.Retrieve(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("memory: recall: %w", err)
	}

	return result, nil
}

func (o *Orchestrator) Summarize(ctx context.Context, memories []Memory) (string, error) {
	if len(memories) == 0 {
		return "", nil
	}

	var contents []string
	for _, m := range memories {
		contents = append(contents, m.Content)
	}

	joined := ""
	for i, c := range contents {
		joined += fmt.Sprintf("%d. %s\n", i+1, c)
	}

	resp, err := o.provider.Complete(ctx, providers.Request{
		Model:        o.model,
		SystemPrompt: "Summarize the following memories into a concise overview. Preserve key facts, decisions, and patterns.",
		Messages: []providers.Message{
			{Role: "user", Content: joined},
		},
		MaxTokens:   500,
		Temperature: 0.3,
	})
	if err != nil {
		return "", fmt.Errorf("memory: summarize: %w", err)
	}

	return resp.Content, nil
}

func (o *Orchestrator) ExtractMemories(ctx context.Context, content string, sessionID string) ([]Memory, error) {
	resp, err := o.provider.Complete(ctx, providers.Request{
		Model:        o.model,
		SystemPrompt: `Extract notable memories from this conversation. For each memory provide: type (episodic, semantic, or procedural), content (concise description), and tags (relevant labels). Output a JSON array of objects with "type", "content", and "tags" fields. If nothing notable, return an empty array.`,
		Messages: []providers.Message{
			{Role: "user", Content: content},
		},
		MaxTokens:   1000,
		Temperature: 0.2,
	})
	if err != nil {
		return nil, fmt.Errorf("memory: extract: %w", err)
	}

	var extracted []struct {
		Type    string   `json:"type"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}

	if err := json.Unmarshal([]byte(resp.Content), &extracted); err != nil {
		return nil, fmt.Errorf("memory: extract unmarshal: %w", err)
	}

	memories := make([]Memory, 0, len(extracted))
	for _, e := range extracted {
		if e.Content == "" {
			continue
		}

		memType := MemoryType(e.Type)
		switch memType {
		case MemoryEpisodic, MemorySemantic, MemoryProcedural:
		default:
			memType = MemorySemantic
		}

		if e.Tags == nil {
			e.Tags = []string{}
		}

		m := &Memory{
			Type:      memType,
			Content:   e.Content,
			Tags:      e.Tags,
			SessionID: sessionID,
			Timestamp: time.Now().UTC(),
			Metadata:  make(map[string]string),
		}

		if err := o.store.Store(ctx, m); err != nil {
			return nil, fmt.Errorf("memory: extract store: %w", err)
		}

		memories = append(memories, *m)
	}

	return memories, nil
}
