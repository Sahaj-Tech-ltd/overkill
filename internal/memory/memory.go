// orphan: embeddings-backed recall (master plan §6.x); needs Python bridge for semantic search
package memory

import (
	"context"
	"errors"
	"time"
)

type MemoryType string

const (
	MemoryEpisodic   MemoryType = "episodic"
	MemorySemantic   MemoryType = "semantic"
	MemoryProcedural MemoryType = "procedural"
)

type Memory struct {
	ID        string            `json:"id"`
	Type      MemoryType        `json:"type"`
	Content   string            `json:"content"`
	Tags      []string          `json:"tags"`
	SessionID string            `json:"session_id"`
	Timestamp time.Time         `json:"timestamp"`
	Relevance float64           `json:"relevance"`
	Metadata  map[string]string `json:"metadata"`
}

type Query struct {
	Content      string       `json:"content"`
	Types        []MemoryType `json:"types,omitempty"`
	Tags         []string     `json:"tags,omitempty"`
	Limit        int          `json:"limit"`
	MinRelevance float64      `json:"min_relevance"`
}

type SearchResult struct {
	Memories []Memory `json:"memories"`
	Total    int      `json:"total"`
}

type ListOptions struct {
	Type      MemoryType
	SessionID string
	Limit     int
	Offset    int
}

type Store interface {
	Store(ctx context.Context, memory *Memory) error
	Retrieve(ctx context.Context, query Query) (*SearchResult, error)
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*Memory, error)
	List(ctx context.Context, opts ListOptions) ([]Memory, error)
}

var ErrNotFound = errors.New("memory: not found")
