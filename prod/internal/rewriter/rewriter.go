// Package rewriter provides LLM prompt rewriter middleware (master plan §6.x).
// The rewriter strips politeness filler, injects specificity hints, and classifies
// prompt complexity. It is wired through the agent's PromptRewriter interface and
// instantiated by the API layer when the rewriter config section is present.
// Not enabled by default; opt-in via config (see RewriterConfig).
package rewriter

import "context"

type Complexity int

const (
	ComplexitySimple    Complexity = iota
	ComplexityAmbiguous Complexity = iota
	ComplexityComplex   Complexity = iota
)

func (c Complexity) String() string {
	switch c {
	case ComplexitySimple:
		return "simple"
	case ComplexityAmbiguous:
		return "ambiguous"
	case ComplexityComplex:
		return "complex"
	default:
		return "unknown"
	}
}

type RewriteResult struct {
	Original   string     `json:"original"`
	Rewritten  string     `json:"rewritten"`
	Complexity Complexity `json:"complexity"`
	Changed    bool       `json:"changed"`
	Injected   []string   `json:"injected"`
	Stripped   []string   `json:"stripped"`
	Confidence float64    `json:"confidence"`
}

type Rewriter interface {
	Rewrite(ctx context.Context, input string) (*RewriteResult, error)
}
