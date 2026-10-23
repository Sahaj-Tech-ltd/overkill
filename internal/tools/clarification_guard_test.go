package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/stretchr/testify/require"
)

func TestClarificationDoesNotHoldProjectOwnershipWhileHumanResponds(t *testing.T) {
	store, front, back, _, _ := coordinationToolsFixture(t)
	entered := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tool := NewAskUserTool(func(ctx context.Context, _ string, _ []string) (string, int, bool) {
		close(entered)
		<-ctx.Done()
		return "", -1, true
	})
	guard := NewCoordinationExecutionGuard(store, front.ID, front.Workspace)
	go func() {
		_, err := guard(ctx, tool, json.RawMessage(`{"question":"Need a human decision"}`))
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("question never surfaced")
	}
	claimCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	_, err := store.CreateContract(claimCtx, back.ID, coordination.ContractSpec{Name: "backend interface", Endpoint: "GET /users", SchemaVersion: "1", Schema: json.RawMessage(`{}`), AcceptanceCriteria: []string{"responds"}, Workspace: back.Workspace, OwnedPaths: []string{"api"}})
	require.NoError(t, err, "independent peer ownership blocked on a human response")
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("question did not cancel")
	}
}
