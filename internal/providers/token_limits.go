package providers

import "fmt"

// TokenLimitResolver resolves independent context and completion limits for a model.
type TokenLimitResolver func(model string) (contextWindow, outputTokens int, err error)

// TokenLimitsFor keeps the caller's output override separate from model metadata,
// so changing models can re-resolve both limits instead of inheriting old caps.
func TokenLimitsFor(models []Model, outputOverride int) TokenLimitResolver {
	return func(id string) (int, int, error) {
		return ResolveTokenLimits(models, id, outputOverride)
	}
}

// ResolveTokenLimits treats legacy MaxTokens as context capacity only. Unknown
// models use a conservative context estimate and leave output at zero so the
// provider chooses its default. An explicit output override must fit both the
// context and any known model output ceiling.
func ResolveTokenLimits(models []Model, id string, outputOverride int) (contextWindow, outputTokens int, err error) {
	contextWindow = 32768
	for _, model := range models {
		if model.ID != id {
			continue
		}
		if model.ContextWindow < 0 || model.MaxTokens < 0 || model.DefaultMaxTokens < 0 || model.MaxOutputTokens < 0 {
			return 0, 0, fmt.Errorf("model %q: token limits must be non-negative", id)
		}
		if model.ContextWindow > 0 {
			contextWindow = model.ContextWindow
		} else if model.MaxTokens > 0 {
			contextWindow = model.MaxTokens
		}
		if model.MaxOutputTokens >= contextWindow {
			return 0, 0, fmt.Errorf("model %q: max_output_tokens %d must be below context_window %d", id, model.MaxOutputTokens, contextWindow)
		}
		// Older catalogs sometimes copied the context capacity to this field.
		// That is not a usable completion default.
		if model.DefaultMaxTokens > 0 && model.DefaultMaxTokens < contextWindow {
			outputTokens = model.DefaultMaxTokens
		}
		if model.MaxOutputTokens > 0 {
			outputTokens = model.MaxOutputTokens
		}
		break
	}
	if outputOverride < 0 {
		return 0, 0, fmt.Errorf("model %q: max_output_tokens must be non-negative", id)
	}
	if outputOverride >= contextWindow {
		return 0, 0, fmt.Errorf("model %q: max_output_tokens %d must be below context_window %d", id, outputOverride, contextWindow)
	}
	if outputOverride > 0 {
		if outputTokens > 0 && outputOverride > outputTokens {
			return 0, 0, fmt.Errorf("model %q: max_output_tokens %d exceeds model output limit %d", id, outputOverride, outputTokens)
		}
		outputTokens = outputOverride
	}
	return contextWindow, outputTokens, nil
}
