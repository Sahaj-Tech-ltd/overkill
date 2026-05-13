package main

import (
	"context"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	memorypkg "github.com/Sahaj-Tech-ltd/overkill/internal/memory"
)

// memoryRetrieverAdapter bridges the public memory.Orchestrator into the
// tiny agent.MemoryRetriever interface so the agent package stays free of
// the internal/memory import. Best-effort: errors propagate to the agent,
// which already treats retrieval failure as "no memories this turn".
type memoryRetrieverAdapter struct {
	orch *memorypkg.Orchestrator
}

func (m *memoryRetrieverAdapter) Search(ctx context.Context, query string, k int) ([]agent.MemoryHit, error) {
	if m == nil || m.orch == nil {
		return nil, nil
	}
	res, err := m.orch.Recall(ctx, query, k)
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Memories) == 0 {
		return nil, nil
	}
	hits := make([]agent.MemoryHit, 0, len(res.Memories))
	for _, mem := range res.Memories {
		hits = append(hits, agent.MemoryHit{
			ID:    mem.ID,
			Text:  mem.Content,
			Score: mem.Relevance,
		})
	}
	return hits, nil
}

// memoryArchiverAdapter bridges memory.Orchestrator into agent's
// MemoryArchiver interface (master plan §6.1 hot/cold paging). Routes
// through RememberSemantic when embeddings are wired (vector retrieval
// path), falling back to plain Remember (episodic, no vector) otherwise.
type memoryArchiverAdapter struct {
	orch *memorypkg.Orchestrator
}

func (m *memoryArchiverAdapter) Archive(ctx context.Context, sessionID, role, content string) error {
	if m == nil || m.orch == nil {
		return nil
	}
	// Tag the archived turn with the role so future search can
	// distinguish "what the user said" from "what the agent did".
	tags := []string{"compacted", "role:" + role}
	// Prefer the semantic path — that's what makes hot/cold paging
	// useful (vector retrieval). Falls through to plain episodic
	// store when no embedder is wired.
	if _, err := m.orch.RememberSemantic(ctx, content, memorypkg.MemoryEpisodic, tags, sessionID); err != nil {
		return err
	}
	return nil
}
