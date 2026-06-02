package audit

import (
	"context"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// SubAgentRunnerAdapter wraps a subagent.Manager so the completion
// auditor can spawn read-only verification sub-agents with LSP tools.
type SubAgentRunnerAdapter struct {
	Manager *subagent.Manager
	Model   string // model to use for audit sub-agent
	WorkDir string
}

// Run spawns a read-only sub-agent with LSP + grep + git tools to
// semantically verify the agent's claims against the actual diff.
func (a *SubAgentRunnerAdapter) Run(ctx context.Context, prompt string) (string, error) {
	task := subagent.GenericTask{
		GoalStr:     "Verify that the agent's completion claims match reality. Cross-check every claim against the git diff and file contents. Use lsp_references to verify new functions/types actually exist and are called. Use grep to check error handling patterns. Return a JSON report.",
		ContextStr:  prompt,
		ToolsetVal:  []string{"lsp", "grep", "git", "fs", "terminal"}, // read-only tools
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

	return result.Summary, nil
}
