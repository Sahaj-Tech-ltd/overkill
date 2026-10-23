package audit

import (
	"context"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// SubAgentRunnerAdapter wraps a subagent.Manager so the completion
// auditor can spawn verification workers with enforced read-only tools.
type SubAgentRunnerAdapter struct {
	Manager *subagent.Manager
	Model   string // model to use for audit sub-agent
	WorkDir string
}

// Run spawns a read-only sub-agent with file, bounded search, and fixed git tools to
// semantically verify the agent's claims against the actual diff.
func (a *SubAgentRunnerAdapter) Run(ctx context.Context, prompt string) (string, error) {
	if a.Manager == nil {
		return "", fmt.Errorf("audit: worker manager required")
	}
	task := subagent.GenericTask{
		GoalStr:     "Verify that the agent's completion claims match reality. Cross-check every claim against the git diff and file contents. Use fs to read relevant files, audit_search to check definitions and call sites, and audit_git for the actual changes. These are textual checks; no LSP or arbitrary terminal is available. Return a JSON report.",
		ContextStr:  prompt,
		ToolsetVal:  []string{"fs", "audit_search", "audit_git"}, // read-only tools
		ModelVal:    a.Model,
		MaxStepsVal: 8,
	}

	result, err := a.Manager.Spawn(ctx, task)
	if err != nil {
		return "", fmt.Errorf("audit: spawn sub-agent: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("audit: sub-agent error: %s", result.Error)
	}

	if len(result.ToolTrace) == 0 {
		return "", fmt.Errorf("audit: worker returned no executed verification tools")
	}
	for _, trace := range result.ToolTrace {
		if !trace.Success {
			return "", fmt.Errorf("audit: verification tool failed: %s", trace.Tool)
		}
	}
	return result.Summary, nil
}
