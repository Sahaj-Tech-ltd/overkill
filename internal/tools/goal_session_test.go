package tools

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type sessionGoalBackend struct {
	mu    sync.Mutex
	goals map[string]json.RawMessage
}

func (b *sessionGoalBackend) SetWithBudgets(_ context.Context, id, text string, _, _ int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, _ := json.Marshal(map[string]string{"text": text})
	b.goals[id] = raw
	return nil
}
func (b *sessionGoalBackend) Get(_ context.Context, id string) (json.RawMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.goals[id], nil
}
func (b *sessionGoalBackend) SetStatus(_ context.Context, id, status string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	raw, _ := json.Marshal(map[string]string{"status": status})
	b.goals[id] = raw
	return nil
}

func TestSessionGoalQuerierBindsBlankToolIDsAndRejectsCrossSession(t *testing.T) {
	backend := &sessionGoalBackend{goals: make(map[string]json.RawMessage)}
	first := NewSessionGoalQuerier(backend, func() string { return "session-first" })
	second := NewSessionGoalQuerier(backend, func() string { return "session-second" })
	var group sync.WaitGroup
	for _, querier := range []*SessionGoalQuerier{first, second} {
		group.Add(1)
		go func(q *SessionGoalQuerier) {
			defer group.Done()
			require.NoError(t, q.SetWithBudgets(context.Background(), "", "fixture goal", 0, 0))
		}(querier)
	}
	group.Wait()
	require.Len(t, backend.goals, 2)
	require.NotContains(t, backend.goals, "")
	require.Contains(t, backend.goals, "session-first")
	require.Contains(t, backend.goals, "session-second")
	_, err := first.Get(context.Background(), "session-second")
	require.ErrorContains(t, err, "mismatch")
	require.Error(t, first.SetStatus(context.Background(), "session-second", "complete"))
	unbound := NewSessionGoalQuerier(backend, func() string { return "" })
	require.Error(t, unbound.SetWithBudgets(context.Background(), "", "invalid", 0, 0))
}
