package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

type contractAbortExecutor struct {
	run func(context.Context, coordination.Message) (json.RawMessage, error)
}

func (e contractAbortExecutor) Reserve(_ context.Context, _ coordination.Binding) (coordination.Execution, error) {
	return e, nil
}
func (e contractAbortExecutor) Run(ctx context.Context, m coordination.Message) (json.RawMessage, error) {
	return e.run(ctx, m)
}
func (e contractAbortExecutor) Release() {}

func TestCoordinationAgreementGuardCancelsOwnedExecution(t *testing.T) {
	database, _ := testpg.Open(t)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	folder := t.TempDir()
	peers := make([]coordination.Peer, 2)
	for i, name := range []string{"frontend", "backend"} {
		metadata := session.NewSession(folder)
		require.NoError(t, sessions.Create(t.Context(), metadata))
		peers[i], err = store.RegisterSessionPeer(t.Context(), metadata.ID, name, "fixture")
		require.NoError(t, err)
	}
	spec := coordination.ContractSpec{Name: "Users", Endpoint: "GET /users", SchemaVersion: "1", Schema: json.RawMessage(`{"type":"object"}`), AcceptanceCriteria: []string{"parses response"}, Workspace: peers[0].Workspace}
	contract, err := store.CreateContract(t.Context(), peers[0].ID, spec)
	require.NoError(t, err)
	_, err = store.AcknowledgeContract(t.Context(), peers[1].ID, contract.ID, 1)
	require.NoError(t, err)
	message, err := store.Send(t.Context(), peers[0].ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`"write client"`), IdempotencyKey: "guard-abort", RequiredContracts: []coordination.ContractDependency{{ContractID: contract.ID, Revision: 1}}})
	require.NoError(t, err)
	observed := make(chan error, 1)
	executor := contractAbortExecutor{run: func(ctx context.Context, m coordination.Message) (json.RawMessage, error) {
		if m.ID != message.ID {
			return json.RawMessage(`"ignored"`), nil
		}
		changed := spec
		changed.SchemaVersion = "2"
		changed.CompatibilityChanges = []coordination.CompatibilityChange{{Kind: "breaking", FromVersion: "1", ToVersion: "2", Description: "response changed"}}
		if _, err := store.UpdateContract(t.Context(), peers[0].ID, contract.ID, 1, changed); err != nil {
			observed <- err
			return nil, err
		}
		owned, abort := context.WithCancelCause(ctx)
		defer abort(nil)
		owned = WithCoordinationAbort(owned, abort)
		guard := NewCoordinationExecutionGuard(store, peers[1].ID, folder)
		_, err := guard(owned, NewFSTool(folder), coordInput(t, FSInput{Action: "write", Path: "client.txt", Content: "stale implementation"}))
		if !errors.Is(err, coordination.ErrRequestContracts) {
			observed <- err
			return nil, err
		}
		observed <- context.Cause(owned)
		return nil, context.Cause(owned)
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	dispatcher := coordination.Dispatcher{Store: store, Executor: executor, Options: coordination.DispatchOptions{PollInterval: 10 * time.Millisecond, LeaseTTL: time.Second}}
	go func() { done <- dispatcher.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("dispatcher cleanup did not finish")
		}
	})
	select {
	case err := <-observed:
		require.ErrorIs(t, err, coordination.ErrRequestContracts)
	case <-time.After(5 * time.Second):
		t.Fatal("guard did not abort owned execution")
	}
	require.Eventually(t, func() bool {
		m, err := store.Read(t.Context(), peers[0].ID, message.ID)
		return err == nil && m.State == coordination.Failed
	}, 5*time.Second, 10*time.Millisecond)
	_, err = os.Stat(filepath.Join(folder, "client.txt"))
	require.True(t, os.IsNotExist(err), "stale agreement must prevent the file effect")
	cancel()

}
