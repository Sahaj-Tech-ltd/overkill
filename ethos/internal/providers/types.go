package providers

import (
	"context"
	"encoding/json"
)

type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request) (<-chan Chunk, error)
	Models() []Model
}

type Request struct {
	Model        string
	Messages     []Message
	Tools        []Tool
	MaxTokens    int
	Temperature  float64
	SystemPrompt string
	Metadata     map[string]any
}

type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type Response struct {
	ID        string
	Model     string
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

type Usage struct {
	InputTokens       int
	OutputTokens      int
	CachedInputTokens int
}

type Chunk struct {
	Content   string
	ToolCalls []ToolCall
	Done      bool
	Usage     *Usage
}

type Model struct {
	ID                string
	Name              string
	Family            string
	MaxTokens         int
	ContextWindow     int
	DefaultMaxTokens  int
	CostIn            float64
	CostOut           float64
	CostCacheIn       float64
	CostCacheOut      float64
	SupportsTools     bool
	SupportsStreaming bool
	SupportsVision    bool
	Reasoning         bool
	StructuredOutput  bool
	Temperature       bool
	Attachment        bool
	OpenWeights       bool
	ReleaseDate       string
	LastUpdated       string
	Knowledge         string
	Status            string
	InputModalities   []string
	OutputModalities  []string
}
