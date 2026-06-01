package main

import (
	"context"
	"encoding/json"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
)

// goalStoreAdapter wraps *agent.GoalStore so it satisfies
// tools.GoalStoreBackend.  The only mismatch is the Get method:
// GoalStore.Get returns *Goal, but GoalStoreBackend.Get must return
// json.RawMessage.  We delegate to GoalStore.GetJSON for that.
type goalStoreAdapter struct {
	gs *agent.GoalStore
}

func (a goalStoreAdapter) SetWithBudgets(ctx context.Context, sessionID, text string, tokenBudget, timeBudget int) error {
	return a.gs.SetWithBudgets(ctx, sessionID, text, tokenBudget, timeBudget)
}

func (a goalStoreAdapter) Get(ctx context.Context, sessionID string) (json.RawMessage, error) {
	return a.gs.GetJSON(ctx, sessionID)
}

func (a goalStoreAdapter) SetStatus(ctx context.Context, sessionID, status string) error {
	return a.gs.SetStatus(ctx, sessionID, status)
}
