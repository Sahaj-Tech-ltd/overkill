package agent

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// VariantResult captures one variant's response.
type VariantResult struct {
	Model     string  `json:"model"`
	Response  string  `json:"response"`
	Tokens    int     `json:"tokens"`
	CostUSD   float64 `json:"cost_usd"`
	Err       string  `json:"error,omitempty"`
	DurationS float64 `json:"duration_s"`
	// Note carries an out-of-band human-readable annotation about this
	// result. Currently used to flag variants where pricing data is missing
	// (so the dialog can show "no pricing data" instead of "$0.0000").
	Note string `json:"note,omitempty"`
}

// modelPricer resolves the per-1M-token cost for a model id. Defaults to
// providers.BuiltinModelByID and is overridable from tests.
var modelPricer = func(id string) (in, out float64, ok bool) {
	m := providers.BuiltinModelByID(id)
	if m == nil {
		return 0, 0, false
	}
	return m.CostIn, m.CostOut, true
}

// computeVariantCost converts (input_tokens, output_tokens, model id) into
// USD using the catalog. Returns (cost, note) where note is non-empty when
// the model has no pricing data.
func computeVariantCost(model string, inputTokens, outputTokens int) (float64, string) {
	in, out, ok := modelPricer(model)
	if !ok {
		return 0, "no pricing data"
	}
	cost := (float64(inputTokens)/1e6)*in + (float64(outputTokens)/1e6)*out
	return cost, ""
}

// RunVariants runs the same prompt against each model in parallel using the
// agent's existing provider, sharing the current history but not mutating it.
//
// Tools are intentionally not invoked — variants are for model output
// comparison, not for diverging tool execution.
func (a *Agent) RunVariants(ctx context.Context, prompt string, models []string) ([]VariantResult, error) {
	if len(models) == 0 {
		return nil, fmt.Errorf("agent: no models supplied")
	}
	a.mu.RLock()
	prov := a.provider
	hist := append([]providers.Message(nil), a.history...)
	sysPrompt := a.systemPrompt
	maxTok := a.maxTokens
	a.mu.RUnlock()

	if prov == nil {
		return nil, fmt.Errorf("agent: no provider configured")
	}

	results := make([]VariantResult, len(models))
	var wg sync.WaitGroup
	for i, model := range models {
		wg.Add(1)
		go func(i int, model string) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("agent: variant goroutine panic: %v\n%s", r, debug.Stack())
				}
			}()
			results[i] = runOneVariant(ctx, prov, sysPrompt, maxTok, model, hist, prompt)
		}(i, model)
	}
	wg.Wait()
	return results, nil
}

func runOneVariant(
	ctx context.Context,
	prov providers.Provider,
	sysPrompt string,
	maxTokens int,
	model string,
	hist []providers.Message,
	prompt string,
) VariantResult {
	msgs := make([]providers.Message, 0, len(hist)+1)
	msgs = append(msgs, hist...)
	msgs = append(msgs, providers.Message{Role: "user", Content: prompt})

	start := time.Now()
	resp, err := prov.Complete(ctx, providers.Request{
		Model:        model,
		Messages:     msgs,
		MaxTokens:    maxTokens,
		SystemPrompt: sysPrompt,
	})
	dur := time.Since(start).Seconds()
	if err != nil {
		return VariantResult{Model: model, Err: err.Error(), DurationS: dur}
	}
	cost, note := computeVariantCost(model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
	return VariantResult{
		Model:     model,
		Response:  resp.Content,
		Tokens:    resp.Usage.InputTokens + resp.Usage.OutputTokens,
		CostUSD:   cost,
		Note:      note,
		DurationS: dur,
	}
}
