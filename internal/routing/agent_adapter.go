// Package routing — adapter that lets a SmartRouter satisfy the
// agent.ModelRouter contract without the agent package importing routing.
package routing

import (
	"context"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
)

// AgentAdapter wraps SmartRouter so its Route() output is consumable by the
// agent's ModelRouter interface.
type AgentAdapter struct {
	router *SmartRouter
}

// NewAgentAdapter wraps r. Returns nil when r is nil so the agent can
// short-circuit cleanly.
func NewAgentAdapter(r *SmartRouter) *AgentAdapter {
	if r == nil {
		return nil
	}
	return &AgentAdapter{router: r}
}

// PickModel runs the underlying classifier+router and returns the chosen
// model ID. Failures return ok=false so the caller falls back to its static
// model.
func (a *AgentAdapter) PickModel(snap agent.RouteSnapshot) (string, string, bool) {
	if a == nil || a.router == nil {
		return "", "", false
	}
	req := RouteRequest{
		UserInput:      snap.UserInput,
		HistoryLength:  snap.HistoryLen,
		ToolCallCount:  snap.ToolCallCount,
		HasAttachments: snap.HasAttachments,
		CodeBlockCount: countCodeBlocks(snap.UserInput),
		EstimatedTokens: estimateTokens(snap.UserInput),
	}
	res, err := a.router.Route(context.Background(), req)
	if err != nil || res == nil {
		return "", "", false
	}
	return res.ModelID, res.Reason, true
}

// countCodeBlocks counts ``` fences (paired); single fences count as one
// open block so the classifier still treats it as code-bearing.
func countCodeBlocks(s string) int {
	return strings.Count(s, "```") / 2
}

// estimateTokens is a cheap word-count proxy. Routing only needs an order of
// magnitude — actual token estimation lives in internal/tokenizer.
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Fields(s)) * 4 / 3
}
