package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestUsageReconciliationAuthenticatedHTTPMetadataAndCommitContract(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	folder := t.TempDir()
	var peers []coordination.Peer
	var chats []*session.Session
	for _, name := range []string{"front", "back", "worker"} {
		chat := session.NewSession(folder)
		require.NoError(t, sessions.Create(t.Context(), chat))
		peer, err := store.RegisterSessionPeer(t.Context(), chat.ID, name, "fixture")
		require.NoError(t, err)
		peers = append(peers, peer)
		chats = append(chats, chat)
	}
	root, err := store.SendWithBudget(t.Context(), peers[0].ID, coordination.SendInput{To: "back", Kind: "request", TaskID: "usage-api", Payload: json.RawMessage(`"root"`), IdempotencyKey: "root"}, coordination.UsageLimits{Tokens: 1000})
	require.NoError(t, err)
	childReady := make(chan coordination.Message, 1)
	dispatchErrors := make(chan error, 8)
	dispatcher := &coordination.Dispatcher{Store: store, Executor: &reconciliationLineageExecutor{store: store, peerID: peers[1].ID, child: childReady}, Options: coordination.DispatchOptions{PollInterval: 10 * time.Millisecond, LeaseTTL: time.Second, Concurrency: 1, OnError: func(_ string, err error) {
		if errors.Is(err, coordination.ErrUsageBudget) {
			return
		}
		select {
		case dispatchErrors <- err:
		default:
		}
	}}}
	dispatchCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- dispatcher.Run(dispatchCtx) }()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-stopped
		}
	}()
	var child coordination.Message
	select {
	case child = <-childReady:
	case err := <-dispatchErrors:
		t.Fatalf("dispatcher child fixture failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("dispatcher did not create child")
	}
	require.Eventually(t, func() bool {
		current, e := store.Read(t.Context(), peers[0].ID, root.ID)
		if e == nil && current.State == "blocked" {
			root = current
			return true
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	<-stopped
	joined = true
	child, err = store.Claim(t.Context(), peers[2].ID)
	require.NoError(t, err)
	child, err = store.Transition(t.Context(), peers[2].ID, child.ID, child.Revision, "blocked", json.RawMessage(`{}`))
	require.NoError(t, err)
	// Seed stopped-journal uncertainty here; real provider omissions are exercised
	// separately by mandatory native acceptance, rather than claiming this is one.
	for _, message := range []coordination.Message{child} {
		_, err = database.Exec(`INSERT INTO coordination_usage_runs(message_id,owner_id) VALUES($1,$2)`, message.ID, uuid.NewString())
		require.NoError(t, err)
	}
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, CoordStore: store})
	startHTTPServer(t, server)
	page := sidebarRPC(t, server, "coordination.reconciliation.candidates", map[string]any{"session_id": chats[0].ID, "message_id": root.ID, "limit": 1, "after_id": ""})
	require.Nil(t, page.Error)
	raw, err := json.Marshal(page.Result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRET CHILD PAYLOAD")
	var first coordination.UsageReconciliationCandidatePage
	require.NoError(t, json.Unmarshal(raw, &first))
	require.Len(t, first.Candidates, 1)
	require.True(t, first.Truncated)
	next := sidebarRPC(t, server, "coordination.reconciliation.candidates", map[string]any{"session_id": chats[0].ID, "message_id": root.ID, "limit": 1, "after_id": first.NextCursor})
	require.Nil(t, next.Error)
	raw, _ = json.Marshal(next.Result)
	var second coordination.UsageReconciliationCandidatePage
	require.NoError(t, json.Unmarshal(raw, &second))
	require.Len(t, second.Candidates, 1)
	require.NotEqual(t, first.Candidates[0].MessageID, second.Candidates[0].MessageID)
	_, err = store.Read(t.Context(), peers[0].ID, child.ID)
	require.Error(t, err, "metadata permission does not reveal hidden child payload")
	snapshotResponse := sidebarRPC(t, server, "coordination.reconciliation", map[string]string{"session_id": chats[0].ID, "message_id": child.ID})
	require.Nil(t, snapshotResponse.Error)
	snapshot, err := store.UsageReconciliationForMessage(t.Context(), peers[0].ID, root.ID)
	require.NoError(t, err)
	input := coordination.ReconcileTaskUsageInput{MessageID: root.ID, ExpectedMessageRevision: snapshot.MessageRevision, ExpectedUsageRevision: snapshot.UsageRevision, ExpectedJournalFingerprint: snapshot.JournalFingerprint, ReconciliationID: uuid.NewString(), InputTokens: 10, OutputTokens: 2, CostUSD: .1, UsageKnown: true, PriceKnown: true, Reason: "Reviewed all attempts", Evidence: "Operator conservative retained-log estimate", Basis: "conservative_estimate", AcknowledgeUncertainty: true}
	wrong := sidebarRPC(t, server, "coordination.reconcile", map[string]any{"session_id": chats[1].ID, "input": input})
	require.NotNil(t, wrong.Error, "nonowner errors must not become typed successful conflict results")
	stale := input
	stale.ExpectedUsageRevision++
	refused := sidebarRPC(t, server, "coordination.reconcile", map[string]any{"session_id": chats[0].ID, "input": stale})
	require.Nil(t, refused.Error)
	raw, _ = json.Marshal(refused.Result)
	require.JSONEq(t, `{"committed":false,"conflict":true}`, string(raw))
	committed := sidebarRPC(t, server, "coordination.reconcile", map[string]any{"session_id": chats[0].ID, "input": input})
	require.Nil(t, committed.Error)
	raw, _ = json.Marshal(committed.Result)
	var receipt struct {
		Committed      bool                                   `json:"committed"`
		Usage          coordination.TaskUsage                 `json:"usage"`
		Reconciliation coordination.UsageReconciliationRecord `json:"reconciliation"`
	}
	require.NoError(t, json.Unmarshal(raw, &receipt))
	require.True(t, receipt.Committed)
	require.Equal(t, "manual_attestation", receipt.Reconciliation.Provenance)
	require.EqualValues(t, 1, receipt.Usage.UnconfirmedExecutions, "other message uncertainty remains")
	current, err := store.Read(t.Context(), peers[0].ID, root.ID)
	require.NoError(t, err)
	require.Equal(t, root.State, current.State)
	require.Equal(t, root.Revision+1, current.Revision)
	again := sidebarRPC(t, server, "coordination.reconcile", map[string]any{"session_id": chats[0].ID, "input": input})
	require.Nil(t, again.Error)
	repeat, _ := json.Marshal(again.Result)
	require.JSONEq(t, string(raw), string(repeat))
	for _, bad := range []string{`null`, `{"session_id":null,"message_id":"x"}`, `{"session_id":"` + chats[0].ID + `","session_id":"forged","message_id":"` + root.ID + `"}`, `{"session_id":"` + chats[0].ID + `","message_id":"` + root.ID + `","owner_peer_id":"forged"}`, `{"session_id":"` + chats[0].ID + `","message_id":"` + root.ID + `"} {}`} {
		_, rpcErr := server.handleUsageReconciliation(t.Context(), "coordination.reconciliation", []byte(bad))
		require.NotNil(t, rpcErr, bad)
	}
	malformed, _ := json.Marshal(map[string]any{"session_id": chats[0].ID, "input": input})
	text := string(malformed)
	text = text[:len(text)-1] + `,"Session_id":"forged"}`
	_, rpcErr := server.handleUsageReconciliation(t.Context(), "coordination.reconcile", []byte(text))
	require.NotNil(t, rpcErr)
}

type reconciliationLineageExecutor struct {
	store  *coordination.Store
	peerID string
	child  chan coordination.Message
}

func (e *reconciliationLineageExecutor) Reserve(_ context.Context, b coordination.Binding) (coordination.Execution, error) {
	if b.Peer.ID != e.peerID {
		return nil, coordination.ErrBusy
	}
	return e, nil
}
func (e *reconciliationLineageExecutor) Release() {}
func (e *reconciliationLineageExecutor) Run(ctx context.Context, m coordination.Message) (json.RawMessage, error) {
	child, err := e.store.Send(ctx, e.peerID, coordination.SendInput{To: "worker", Kind: "request", TaskID: m.TaskID, Payload: json.RawMessage(`"SECRET CHILD PAYLOAD"`), IdempotencyKey: "child"})
	if err != nil {
		return nil, err
	}
	if err = e.store.BeginTaskUsage(ctx, m.ID); err != nil {
		return nil, err
	}
	e.child <- child
	return nil, coordination.ErrUsageBudget
}
