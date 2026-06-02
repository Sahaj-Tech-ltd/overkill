package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GoalSteeringProvider returns a contextProviderFn that injects a steering
// message into the system prompt when the goal status requires it.
//
// If the goal status is "budget_limited", the agent is told to wrap up
// and report completion.
//
// If the goal status is "blocked", the agent is told the task is blocked
// and should not continue.
func GoalSteeringProvider(store *GoalStore, sessionID string) func(ctx context.Context, sessionID string) string {
	return func(ctx context.Context, _ string) string {
		if store == nil {
			return ""
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		g, err := store.Get(cctx, sessionID)
		if err != nil || g == nil || !g.Active {
			return ""
		}
		status := strings.TrimSpace(g.Status)
		switch status {
		case "budget_limited":
			return budgetLimitedSteering(g)
		case "blocked":
			return blockedSteering(g)
		case "complete":
			return completeSteering(g)
		default:
			return ""
		}
	}
}

func budgetLimitedSteering(g *Goal) string {
	return fmt.Sprintf(
		"[GOAL BUDGET EXCEEDED] Token budget of %d has been reached (%d used). "+
			"Stop what you're doing, provide a concise summary of what was accomplished, "+
			"and mark the goal as complete via update_goal if appropriate. "+
			"Goal: %s",
		g.TokenBudget, g.TokensUsed, g.Text,
	)
}

func blockedSteering(g *Goal) string {
	return fmt.Sprintf(
		"[GOAL BLOCKED] The goal '%s' has been marked as blocked. "+
			"Do not continue work on this goal. Explain the blockage to the user "+
			"and wait for further instructions.",
		g.Text,
	)
}

func completeSteering(g *Goal) string {
	return fmt.Sprintf(
		"[GOAL COMPLETE] The goal '%s' has been marked as complete. "+
			"Provide a summary of what was done and total usage: %d tokens, %d seconds.",
		g.Text, g.TokensUsed, g.TimeUsedSeconds,
	)
}
