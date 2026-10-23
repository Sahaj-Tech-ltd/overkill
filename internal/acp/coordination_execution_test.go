package acp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type transportProofExecutor struct {
	run  func(context.Context, coordination.Message) (json.RawMessage, error)
	peer string
}

func (e transportProofExecutor) Reserve(_ context.Context, b coordination.Binding) (coordination.Execution, error) {
	if b.Peer.ID != e.peer {
		return nil, coordination.ErrBusy
	}
	return transportProofExecution{e.run}, nil
}

type transportProofExecution struct {
	run func(context.Context, coordination.Message) (json.RawMessage, error)
}

func (e transportProofExecution) Run(ctx context.Context, m coordination.Message) (json.RawMessage, error) {
	return e.run(ctx, m)
}
func (e transportProofExecution) Release() {}
func TestSameHubTransportExecutionProofPreservesAncestryAndRejectsForgery(t *testing.T) {
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	var peers []coordination.Peer
	var tokens []string
	for _, name := range []string{"frontend", "backend", "worker"} {
		p, token, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "p", Name: name, Role: name, Workspace: t.TempDir()})
		require.NoError(t, err)
		s := session.NewSession(p.Workspace)
		require.NoError(t, sessions.Create(t.Context(), s))
		require.NoError(t, store.BindSession(t.Context(), p.ID, s.ID))
		peers = append(peers, p)
		tokens = append(tokens, token)
	}
	contract, err := store.CreateContract(t.Context(), peers[0].ID, coordination.ContractSpec{Name: "users", Endpoint: "GET /users", SchemaVersion: "1", Schema: json.RawMessage(`{"type":"object"}`), AcceptanceCriteria: []string{"schema"}, Workspace: peers[0].Workspace, OwnedPaths: []string{"schema.json"}})
	require.NoError(t, err)
	_, err = store.AcknowledgeContract(t.Context(), peers[1].ID, contract.ID, contract.Revision)
	require.NoError(t, err)
	root, err := store.Send(t.Context(), peers[0].ID, coordination.SendInput{To: "backend", Kind: "request", TaskID: "same-task", Payload: json.RawMessage(`{}`), IdempotencyKey: "root", Deadline: time.Now().Add(time.Minute), RequiredContracts: []coordination.ContractDependency{{ContractID: contract.ID, Revision: contract.Revision}}})
	require.NoError(t, err)
	server := NewServer(Config{Token: "global", Coordination: store})
	defer server.Shutdown(context.Background())
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := NewClient(httpServer.URL, tokens[1])
	type result struct {
		child                CoordinationResponse
		proof                *coordination.TransportExecution
		forged, cycle, error error
	}
	results := make(chan result, 1)
	executor := transportProofExecutor{peer: peers[1].ID, run: func(ctx context.Context, m coordination.Message) (json.RawMessage, error) {
		proof := coordination.ExecutionTransportProof(ctx)
		if proof == nil {
			return nil, errors.New("missing private runtime proof")
		}
		input := CoordinationRequest{Execution: proof, Send: &coordination.SendInput{To: "worker", Kind: "request", ConversationID: uuid.NewString(), TaskID: "forged-task-reset", Payload: json.RawMessage(`{}`), Deadline: time.Now().Add(time.Hour), IdempotencyKey: "child"}}
		bad := *proof
		bad.Capability = strings.Repeat("0", 64)
		input.Execution = &bad
		input.Send.IdempotencyKey = "forged-capability"
		_, forged := client.Coordination(ctx, "send", input)
		input.Execution = proof
		input.Send.IdempotencyKey = "child"
		child, err := client.Coordination(ctx, "send", input)
		input.Send.To = "frontend"
		input.Send.IdempotencyKey = "cycle"
		_, cycle := client.Coordination(ctx, "send", input)
		results <- result{child, proof, forged, cycle, err}
		return json.RawMessage(`{"ready":true}`), err
	}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- (&coordination.Dispatcher{Store: store, Executor: executor, Options: coordination.DispatchOptions{PollInterval: 10 * time.Millisecond, LeaseTTL: time.Second}}).Run(ctx)
	}()
	defer func() { cancel(); require.NoError(t, <-done) }()
	var observed result
	select {
	case observed = <-results:
	case <-time.After(5 * time.Second):
		t.Fatal("transport worker did not run")
	}
	require.Error(t, observed.forged, "known owner/message IDs alone cannot forge active execution")
	require.NoError(t, observed.error)
	require.Error(t, observed.cycle)
	child := observed.child.Message
	require.NotNil(t, child)
	require.Equal(t, root.ID, child.ParentMessageID)
	require.Equal(t, root.ConversationID, child.ConversationID)
	require.Equal(t, root.TaskID, child.TaskID)
	require.True(t, root.Deadline.Equal(child.Deadline))
	require.Equal(t, root.RequestDepth+1, child.RequestDepth)
	require.Equal(t, root.RequiredContracts, child.RequiredContracts)
	require.Eventually(t, func() bool {
		m, err := store.Read(t.Context(), peers[0].ID, root.ID)
		return err == nil && m.State == coordination.Completed
	}, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		_, err := client.Coordination(t.Context(), "send", CoordinationRequest{Execution: observed.proof, Send: &coordination.SendInput{To: "worker", Kind: "request", Payload: json.RawMessage(`{}`), IdempotencyKey: "stale-proof"}})
		return err != nil
	}, time.Second, 10*time.Millisecond)
	evidence, err := store.ReadEscalations(t.Context(), peers[1].ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, evidence, 1)
	require.Equal(t, "request_cycle", evidence[0].Reason)
	require.Equal(t, coordination.Blocked, evidence[0].State)
	output, _ := json.Marshal(observed.child)
	require.NotContains(t, string(output), observed.proof.Capability, "private capability cannot appear in model-visible receipt")
}
