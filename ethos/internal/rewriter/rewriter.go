// orphan: LLM prompt rewriter middleware (master plan §6.x); not enabled by default
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
	Original   string   `json:"original"`
	Rewritten  string   `json:"rewritten"`
	Complexity Complexity `json:"complexity"`
	Changed    bool     `json:"changed"`
	Injected   []string `json:"injected"`
	Stripped   []string `json:"stripped"`
	Confidence float64  `json:"confidence"`
}

type Rewriter interface {
	Rewrite(ctx context.Context, input string) (*RewriteResult, error)
}
