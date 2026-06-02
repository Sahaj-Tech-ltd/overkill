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
	Model         string
	Messages      []Message
	Tools         []Tool
	MaxTokens     int
	Temperature   float64
	SystemPrompt  string
	ThinkingLevel string // off|minimal|low|medium|high|x-high — passed through from config
	Metadata      map[string]any
}

type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	// Attachments carry non-text inputs (images today, potentially
	// audio/PDF later). Empty for the common case so a string Content
	// still serializes as a plain string-content message to every
	// provider. When non-empty, the provider's request builder emits
	// the format-specific multi-part body (anthropic content blocks,
	// OpenAI content parts, gemini inlineData). Order is preserved.
	Attachments []Attachment
}

// AttachmentKind enumerates the modalities the agent can submit to a
// provider. "image" is the only supported value today; the type stays
// open so audio/PDF can land without re-shaping every call site.
type AttachmentKind string

const (
	AttachmentImage AttachmentKind = "image"
)

// Attachment is a single non-text payload bound to a Message. Data is
// the raw bytes (the provider layer base64-encodes when the API needs
// it — keeping bytes in memory lets us size-check before send without
// double-encoding). MediaType is the IANA media type (e.g. "image/png");
// providers reject unknown types so we don't paper over a clipboard
// read that returned the wrong MIME.
type Attachment struct {
	Kind      AttachmentKind
	MediaType string
	Data      []byte
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
	// Err carries a fatal streaming error (transport drop, scanner failure,
	// parse error on the upstream protocol). Consumers MUST treat a non-nil
	// Err as "this stream did not complete cleanly" — committing the
	// accumulated content as if Done would be a silent wrong answer. The
	// producer emits at most one Err chunk and then closes the channel.
	Err error
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
