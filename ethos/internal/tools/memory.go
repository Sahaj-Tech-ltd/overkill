// Package tools — memory_* tools wrap the memory.Orchestrator (master plan
// §6.1). Each tool is nil-safe: when the Orchestrator hasn't been wired the
// tool returns a polite "memory not configured" message rather than panicking.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/ethos/internal/memory"
)

// MemoryRememberTool stores a memory and (when the bridge is wired) its embedding.
type MemoryRememberTool struct {
	orch *memory.Orchestrator
}

func NewMemoryRememberTool(o *memory.Orchestrator) *MemoryRememberTool {
	return &MemoryRememberTool{orch: o}
}

func (t *MemoryRememberTool) Name() string { return "memory_remember" }

type memoryRememberInput struct {
	Content   string   `json:"content"`
	Type      string   `json:"type,omitempty"` // episodic | semantic | procedural
	Tags      []string `json:"tags,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
}

func (t *MemoryRememberTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.orch == nil {
		return errorJSON("memory not configured"), nil
	}
	var req memoryRememberInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("memory_remember: %w", err)
	}
	if req.Content == "" {
		return errorJSON("content is required"), nil
	}
	mt := memory.MemoryType(req.Type)
	switch mt {
	case memory.MemoryEpisodic, memory.MemorySemantic, memory.MemoryProcedural:
	default:
		mt = memory.MemorySemantic
	}
	m, err := t.orch.RememberSemantic(ctx, req.Content, mt, req.Tags, req.SessionID)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{
		"id":         m.ID,
		"semantic":   t.orch.HasEmbeddings(),
		"embed_err":  m.Metadata["embed_error"],
		"vector_err": m.Metadata["vector_error"],
	})
	return out, nil
}

// MemoryRecallTool searches stored memories by relevance.
type MemoryRecallTool struct {
	orch *memory.Orchestrator
}

func NewMemoryRecallTool(o *memory.Orchestrator) *MemoryRecallTool {
	return &MemoryRecallTool{orch: o}
}

func (t *MemoryRecallTool) Name() string { return "memory_recall" }

type memoryRecallInput struct {
	Query string   `json:"query"`
	TopK  int      `json:"top_k,omitempty"`
	Types []string `json:"types,omitempty"` // episodic | semantic | procedural
	Tags  []string `json:"tags,omitempty"`
}

func (t *MemoryRecallTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.orch == nil {
		return errorJSON("memory not configured"), nil
	}
	var req memoryRecallInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("memory_recall: %w", err)
	}
	if req.Query == "" {
		return errorJSON("query is required"), nil
	}
	if req.TopK <= 0 {
		req.TopK = 10
	}
	res, err := t.orch.SemanticRecall(ctx, req.Query, req.TopK)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	// Post-filter by type / tags (the underlying SemanticRecall fallback
	// hits Retrieve which already supports these, but the bridge path does
	// not — so we filter here for parity).
	if len(req.Types) > 0 || len(req.Tags) > 0 {
		filtered := make([]memory.Memory, 0, len(res.Memories))
		for _, m := range res.Memories {
			if len(req.Types) > 0 && !typeIn(req.Types, string(m.Type)) {
				continue
			}
			if len(req.Tags) > 0 && !tagsIntersect(req.Tags, m.Tags) {
				continue
			}
			filtered = append(filtered, m)
		}
		res.Memories = filtered
		res.Total = len(filtered)
	}
	out, _ := json.Marshal(res)
	return out, nil
}

func typeIn(want []string, got string) bool {
	for _, w := range want {
		if w == got {
			return true
		}
	}
	return false
}

func tagsIntersect(want []string, got []string) bool {
	set := make(map[string]struct{}, len(got))
	for _, g := range got {
		set[g] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; ok {
			return true
		}
	}
	return false
}

// MemoryForgetTool deletes a stored memory (and its vector if applicable).
type MemoryForgetTool struct {
	orch *memory.Orchestrator
}

func NewMemoryForgetTool(o *memory.Orchestrator) *MemoryForgetTool {
	return &MemoryForgetTool{orch: o}
}

func (t *MemoryForgetTool) Name() string { return "memory_forget" }

type memoryForgetInput struct {
	ID string `json:"id"`
}

func (t *MemoryForgetTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.orch == nil {
		return errorJSON("memory not configured"), nil
	}
	var req memoryForgetInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("memory_forget: %w", err)
	}
	if req.ID == "" {
		return errorJSON("id is required"), nil
	}
	if err := t.orch.ForgetSemantic(ctx, req.ID); err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"ok": true})
	return out, nil
}
