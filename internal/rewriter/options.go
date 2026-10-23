package rewriter

import (
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// Options independently controls local and provider-assisted transformations.
// A disabled rewriter is an exact passthrough, including whitespace.
type Options struct {
	Enabled          bool
	StripSycophancy  bool
	AntiPatternGuard bool
	LLMRewrite       bool
}

// NewConfiguredRewriter is the shared CLI/API config mapping. In particular,
// enabling the middleware alone does not opt into paid provider calls.
func NewConfiguredRewriter(provider providers.Provider, fallbackModel string, cfg config.RewriterConfig) *LLMRewriter {
	model := cfg.Model
	if model == "" {
		model = fallbackModel
	}
	return NewWithOptions(provider, model, Options{Enabled: cfg.Enabled, StripSycophancy: cfg.StripSycophancy, AntiPatternGuard: cfg.AntiPatternGuard, LLMRewrite: cfg.LLMRewrite})
}

func NewWithOptions(provider providers.Provider, model string, opts Options) *LLMRewriter {
	return &LLMRewriter{provider: provider, model: model, middleware: NewMiddleware(), reducer: NewSycophancyReducer(), guard: newAntiPatternGuard(), options: opts}
}
