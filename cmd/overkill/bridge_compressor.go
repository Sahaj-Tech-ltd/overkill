package main

import (
	"context"

	"github.com/Sahaj-Tech-ltd/overkill/bridge"
)

// bridgeCompressorAdapter satisfies agent.PromptCompressor by routing
// compression through the Python bridge's Compact RPC (master plan §4.4
// "cheapest available model" path). When the bridge is up this avoids
// burning main-model tokens on prompt shrinking.
//
// targetTokens is left at 0 to let the Python side pick a default based
// on the supplied content length. style="lossy" matches LLMLingua-style
// salience compression; the bridge implementation decides whether to
// honour that exactly.
type bridgeCompressorAdapter struct {
	client *bridge.Client
	model  string
}

func (b *bridgeCompressorAdapter) Compress(ctx context.Context, prompt string) (string, int, error) {
	if b == nil || b.client == nil {
		return prompt, 0, nil
	}
	out, origToks, compToks, err := b.client.Compact(ctx, prompt, b.model, 0, "lossy")
	if err != nil {
		// Best-effort: surface the error so the agent's PromptCompressor
		// fallback (defined in internal/agent) returns the original
		// prompt unchanged. The agent never blocks a turn on this.
		return prompt, 0, err
	}
	saved := int(origToks - compToks)
	if saved < 0 {
		saved = 0
	}
	return out, saved, nil
}
