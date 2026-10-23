package coordination

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func contractRequest(t *testing.T, s *Store, from Peer, c Contract, key string) Message {
	t.Helper()
	in := request(key)
	in.RequiredContracts = []ContractDependency{{ContractID: c.ID, Revision: c.Revision}}
	m, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	return m
}
func TestRequestContractsRestartAckGateAndIndependentWork(t *testing.T) {
	s, from, back, _ := fixture(t)
	c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "gate"))
	require.NoError(t, err)
	m := contractRequest(t, s, from, c, "gated")
	in := request("plain")
	plain, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	fresh, err := NewStore(t.Context(), s.db)
	require.NoError(t, err)
	got, err := fresh.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, plain.ID, got.ID)
	finishDependency(t, fresh, back, got)
	_, err = fresh.Claim(t.Context(), back.ID)
	require.ErrorIs(t, err, ErrNotFound)
	graph, err := fresh.Graph(t.Context(), from.ID, m.ID, 100)
	require.NoError(t, err)
	require.Equal(t, "waiting", graph.Nodes[0].Readiness)
	require.Len(t, graph.Nodes[0].ContractBlockers, 1)
	_, err = s.AcknowledgeContract(t.Context(), back.ID, c.ID, c.Revision)
	require.NoError(t, err)
	got, err = fresh.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, m.ID, got.ID)
	require.Equal(t, m.RequiredContracts, got.RequiredContracts)
	finishDependency(t, s, back, got)
}
func TestRequestContractsStaleDeletedAndCascadedFailure(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "revision", true: "deletion"}[deleted], func(t *testing.T) {
			s, from, back, _ := fixture(t)
			worker := dependencyWorker(t, s)
			c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "gate"))
			require.NoError(t, err)
			m := contractRequest(t, s, from, c, "gated")
			child := sendDependency(t, s, from, worker.Name, "child", m.ID)
			if deleted {
				require.NoError(t, s.DeleteContract(t.Context(), from.ID, c.ID, 1))
			} else {
				_, err = s.UpdateContract(t.Context(), from.ID, c.ID, 1, c.Spec)
				require.NoError(t, err)
			}
			_, err = s.Claim(t.Context(), back.ID)
			require.ErrorIs(t, err, ErrNotFound)
			got, err := s.Read(t.Context(), from.ID, m.ID)
			require.NoError(t, err)
			require.Equal(t, Failed, got.State)
			require.Zero(t, got.ExecutionAttempts)
			_, err = s.Claim(t.Context(), worker.ID)
			require.ErrorIs(t, err, ErrNotFound)
			got, err = s.Read(t.Context(), from.ID, child.ID)
			require.NoError(t, err)
			require.Equal(t, Failed, got.State)
			events, err := s.ReadEscalations(t.Context(), from.ID, 0, 100)
			require.NoError(t, err)
			require.Len(t, events, 2)
		})
	}
}
func TestRequestContractsGuardAndAllCompletionPathsRejectChangedAgreement(t *testing.T) {
	for _, path := range []string{"worker", "reply", "transition"} {
		t.Run(path, func(t *testing.T) {
			s, from, back, _ := fixture(t)
			c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "gate"))
			require.NoError(t, err)
			_, err = s.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
			require.NoError(t, err)
			m := contractRequest(t, s, from, c, "gated")
			lease, err := s.AcquireLease(t.Context(), back.ID, uuid.NewString(), time.Minute)
			require.NoError(t, err)
			m, err = s.ClaimLease(t.Context(), lease)
			require.NoError(t, err)
			ctx := withExecutionLineage(t.Context(), m, lease)
			_, err = s.UpdateContract(t.Context(), from.ID, c.ID, 1, c.Spec)
			require.NoError(t, err)
			called := false
			require.ErrorIs(t, s.WithOwnedPaths(ctx, back.ID, []string{"out.txt"}, func(context.Context) error { called = true; return nil }), ErrRequestContracts)
			require.ErrorIs(t, s.WithUnrestrictedWorkspace(ctx, back.ID, func(context.Context) error { called = true; return nil }), ErrRequestContracts)
			require.False(t, called)
			switch path {
			case "worker":
				_, err = s.CompleteLease(ctx, lease, m.ID, json.RawMessage(`{"result":"no"}`))
			case "transition":
				_, err = s.Transition(ctx, back.ID, m.ID, m.Revision, "completed", json.RawMessage(`{}`))
			case "reply":
				in := request("reply")
				in.To = from.Name
				in.Kind = "answer"
				in.InReplyTo = m.ID
				_, err = s.Send(ctx, back.ID, in)
			}
			require.ErrorIs(t, err, ErrRequestContracts)
			got, err := s.Read(t.Context(), from.ID, m.ID)
			require.NoError(t, err)
			require.Equal(t, Failed, got.State)
			var answers int
			require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM coordination_messages WHERE in_reply_to=$1 AND kind='answer'`, m.ID).Scan(&answers))
			require.Zero(t, answers)
		})
	}
}
func TestRequestContractsValidationScopeAndIdempotentReceipts(t *testing.T) {
	s, from, back, _ := fixture(t)
	c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "gate"))
	require.NoError(t, err)
	in := request("legacy")
	m, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	in.RequiredContracts = []ContractDependency{}
	got, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	require.Equal(t, m.ID, got.ID)
	in = request("pinned")
	in.RequiredContracts = []ContractDependency{{c.ID, 1}}
	m, err = s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	require.NoError(t, s.DeleteContract(t.Context(), from.ID, c.ID, 1))
	got, err = s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	require.Equal(t, m.ID, got.ID)
	in.RequiredContracts[0].Revision = 2
	_, err = s.Send(t.Context(), from.ID, in)
	require.ErrorIs(t, err, ErrIdempotency)
	for _, pins := range [][]ContractDependency{{{uuid.NewString(), 0}}, {{"bad", 1}}, {{uuid.NewString(), 1}, {uuid.NewString(), -1}}, {{m.ID, 1}, {m.ID, 1}}} {
		in = request(uuid.NewString())
		in.RequiredContracts = pins
		_, err = s.Send(t.Context(), from.ID, in)
		require.Error(t, err)
	}
	outsider, _, err := s.RegisterPeer(t.Context(), Peer{ProjectID: "elsewhere", Name: "outsider", Role: "test", Workspace: t.TempDir()})
	require.NoError(t, err)
	other, err := s.CreateContract(t.Context(), outsider.ID, contractSpec(outsider, "foreign"))
	require.NoError(t, err)
	in = request("foreign")
	in.RequiredContracts = []ContractDependency{{other.ID, 1}}
	_, err = s.Send(t.Context(), from.ID, in)
	require.ErrorIs(t, err, ErrNotFound)
	in.Kind = "progress"
	in.To = back.Name
	_, err = s.Send(t.Context(), from.ID, in)
	require.Error(t, err)
}

func TestRequestContractsRecursivePinsAndGuardSerializesConcurrentMutation(t *testing.T) {
	s, from, back, _ := fixture(t)
	upstream, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "upstream"))
	require.NoError(t, err)
	spec := contractSpec(from, "downstream")
	spec.Dependencies = []ContractDependency{{upstream.ID, 1}}
	downstream, err := s.CreateContract(t.Context(), from.ID, spec)
	require.NoError(t, err)
	_, err = s.AcknowledgeContract(t.Context(), back.ID, downstream.ID, 1)
	require.NoError(t, err)
	m := contractRequest(t, s, from, downstream, "recursive")
	_, err = s.Claim(t.Context(), back.ID)
	require.ErrorIs(t, err, ErrNotFound, "dependent owner must acknowledge upstream")
	_, err = s.AcknowledgeContract(t.Context(), from.ID, upstream.ID, 1)
	require.NoError(t, err)
	lease, err := s.AcquireLease(t.Context(), back.ID, uuid.NewString(), time.Minute)
	require.NoError(t, err)
	m, err = s.ClaimLease(t.Context(), lease)
	require.NoError(t, err)
	ctx := withExecutionLineage(t.Context(), m, lease)
	entered := make(chan struct{})
	release := make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- s.WithOwnedPaths(ctx, back.ID, []string{"out.txt"}, func(context.Context) error { close(entered); <-release; return nil })
	}()
	<-entered
	updateDone := make(chan error, 1)
	go func() { _, e := s.UpdateContract(t.Context(), from.ID, upstream.ID, 1, upstream.Spec); updateDone <- e }()
	select {
	case err := <-updateDone:
		t.Fatalf("update passed effect lock: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-guardDone)
	require.NoError(t, <-updateDone)
	require.ErrorIs(t, s.WithOwnedPaths(ctx, back.ID, []string{"out.txt"}, func(context.Context) error { t.Fatal("stale transitive agreement executed"); return nil }), ErrRequestContracts)
	_, err = s.CompleteLease(ctx, lease, m.ID, json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrRequestContracts)
	got, err := s.Read(t.Context(), from.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, Failed, got.State)
}

func TestRequestContractsLostAcknowledgementBlocksCompletionAndResume(t *testing.T) {
	s, from, back, _ := fixture(t)
	c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "gate"))
	require.NoError(t, err)
	_, err = s.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
	require.NoError(t, err)
	m := contractRequest(t, s, from, c, "gated")
	lease, err := s.AcquireLease(t.Context(), back.ID, uuid.NewString(), time.Minute)
	require.NoError(t, err)
	m, err = s.ClaimLease(t.Context(), lease)
	require.NoError(t, err)
	// Simulate persisted acknowledgement loss without changing the pinned revision.
	_, err = s.db.Exec(`DELETE FROM coordination_contract_acknowledgements WHERE contract_id=$1 AND peer_id=$2`, c.ID, back.ID)
	require.NoError(t, err)
	_, err = s.CompleteLease(t.Context(), lease, m.ID, json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrRequestContracts)
	got, err := s.Read(t.Context(), from.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, Blocked, got.State)
	_, err = s.Transition(t.Context(), back.ID, m.ID, got.Revision, "started", json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrRequestContracts)
	got, err = s.Read(t.Context(), from.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, Blocked, got.State)
	_, err = s.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
	require.NoError(t, err)
	resumed, err := s.Transition(t.Context(), back.ID, m.ID, got.Revision, "started", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.Equal(t, Started, resumed.State)
	badLease := lease
	badLease.OwnerID = uuid.NewString()
	ctx := withExecutionLineage(t.Context(), resumed, badLease)
	require.ErrorIs(t, s.WithOwnedPaths(ctx, back.ID, []string{"out.txt"}, func(context.Context) error { t.Fatal("expired lease executed"); return nil }), ErrLeaseLost)
}

func TestDispatcherPreservesContractGateDecisionAfterEveryRunOutcome(t *testing.T) {
	for _, change := range []string{"missing_ack", "owner_disabled", "revision_changed"} {
		for _, outcome := range []string{"success", "guard_error", "cancelled"} {
			t.Run(change+"/"+outcome, func(t *testing.T) {
				s, from, back, _ := fixture(t)
				c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "gate"))
				require.NoError(t, err)
				_, err = s.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
				require.NoError(t, err)
				m := contractRequest(t, s, from, c, "gated")
				entered := make(chan struct{})
				release := make(chan struct{})
				done := make(chan error, 1)
				d := Dispatcher{Store: s, Executor: testExecutor{reserve: func(context.Context, Binding) (Execution, error) {
					return &testExecution{run: func(ctx context.Context, _ Message) (json.RawMessage, error) {
						close(entered)
						<-release
						switch outcome {
						case "guard_error":
							return nil, s.WithOwnedPaths(ctx, back.ID, []string{"out.txt"}, func(context.Context) error { t.Error("invalid agreement permitted effect"); return nil })
						case "cancelled":
							return nil, context.Canceled
						default:
							return json.RawMessage(`{"apparently_successful":true}`), nil
						}
					}}, nil
				}}}
				go func() { done <- d.execute(t.Context(), Binding{Peer: back}, uuid.NewString(), time.Minute) }()
				<-entered
				switch change {
				case "missing_ack":
					_, err = s.db.Exec(`DELETE FROM coordination_contract_acknowledgements WHERE contract_id=$1 AND peer_id=$2`, c.ID, back.ID)
				case "owner_disabled":
					_, err = s.db.Exec(`UPDATE coordination_peers SET enabled=false WHERE id=$1`, from.ID)
				case "revision_changed":
					_, err = s.UpdateContract(t.Context(), from.ID, c.ID, 1, c.Spec)
				}
				require.NoError(t, err)
				close(release)
				require.ErrorIs(t, <-done, ErrRequestContracts)
				got, err := s.Read(t.Context(), back.ID, m.ID)
				require.NoError(t, err)
				want := Blocked
				if change == "revision_changed" {
					want = Failed
				}
				require.Equal(t, want, got.State)
				var answers, escalations, decisions int
				require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM coordination_messages WHERE in_reply_to=$1 AND kind='answer'`, m.ID).Scan(&answers))
				require.Zero(t, answers)
				require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM coordination_escalations WHERE parent_message_id=$1`, m.ID).Scan(&escalations))
				require.Equal(t, 1, escalations)
				require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM coordination_events WHERE message_id=$1 AND type IN ('failed','blocked')`, m.ID).Scan(&decisions))
				require.Equal(t, 1, decisions)
			})
		}
	}
}

func TestDispatcherPreservesGateDecisionAlreadyMadeByConsumedReplyError(t *testing.T) {
	s, from, back, _ := fixture(t)
	c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "gate"))
	require.NoError(t, err)
	_, err = s.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
	require.NoError(t, err)
	m := contractRequest(t, s, from, c, "gated")
	d := Dispatcher{Store: s, Executor: testExecutor{reserve: func(context.Context, Binding) (Execution, error) {
		return &testExecution{run: func(ctx context.Context, _ Message) (json.RawMessage, error) {
			_, e := s.db.Exec(`DELETE FROM coordination_contract_acknowledgements WHERE contract_id=$1 AND peer_id=$2`, c.ID, back.ID)
			require.NoError(t, e)
			in := request("reply")
			in.To = from.Name
			in.Kind = "answer"
			in.InReplyTo = m.ID
			_, e = s.Send(ctx, back.ID, in)
			require.ErrorIs(t, e, ErrRequestContracts)
			return json.RawMessage(`{"consumed_error":true}`), nil
		}}, nil
	}}}
	require.ErrorIs(t, d.execute(t.Context(), Binding{Peer: back}, uuid.NewString(), time.Minute), ErrRequestContracts)
	got, err := s.Read(t.Context(), back.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, Blocked, got.State)
	var escalations int
	require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM coordination_escalations WHERE parent_message_id=$1`, m.ID).Scan(&escalations))
	require.Equal(t, 1, escalations)
}

func TestRequestContractsCausalChildrenCannotDropPinsAcrossTwoRecipients(t *testing.T) {
	s, from, back, _ := fixture(t)
	worker := dependencyWorker(t, s)
	fourth, _, err := s.RegisterPeer(t.Context(), Peer{ProjectID: from.ProjectID, Name: "fourth", Role: "test", Workspace: t.TempDir()})
	require.NoError(t, err)
	c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "gate"))
	require.NoError(t, err)
	_, err = s.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
	require.NoError(t, err)
	root := contractRequest(t, s, from, c, "root")
	backLease, err := s.AcquireLease(t.Context(), back.ID, uuid.NewString(), time.Minute)
	require.NoError(t, err)
	root, err = s.ClaimLease(t.Context(), backLease)
	require.NoError(t, err)
	backCtx := withExecutionLineage(t.Context(), root, backLease)
	childInput := request("child")
	childInput.To = worker.Name
	child, err := s.Send(backCtx, back.ID, childInput)
	require.NoError(t, err)
	require.Equal(t, root.RequiredContracts, child.RequiredContracts)
	_, err = s.Claim(t.Context(), worker.ID)
	require.ErrorIs(t, err, ErrNotFound, "parent's acknowledgement cannot stand in for child recipient")
	_, err = s.AcknowledgeContract(t.Context(), worker.ID, c.ID, 1)
	require.NoError(t, err)
	workerLease, err := s.AcquireLease(t.Context(), worker.ID, uuid.NewString(), time.Minute)
	require.NoError(t, err)
	child, err = s.ClaimLease(t.Context(), workerLease)
	require.NoError(t, err)
	workerCtx := withExecutionLineage(t.Context(), child, workerLease)
	grandInput := request("grandchild")
	grandInput.To = fourth.Name
	grandInput.RequiredContracts = []ContractDependency{}
	grand, err := s.Send(workerCtx, worker.ID, grandInput)
	require.NoError(t, err)
	require.Equal(t, root.RequiredContracts, grand.RequiredContracts)
	_, err = s.Claim(t.Context(), fourth.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.AcknowledgeContract(t.Context(), fourth.ID, c.ID, 1)
	require.NoError(t, err)
	fourthLease, err := s.AcquireLease(t.Context(), fourth.ID, uuid.NewString(), time.Minute)
	require.NoError(t, err)
	grand, err = s.ClaimLease(t.Context(), fourthLease)
	require.NoError(t, err)
	conflicting := request("conflict")
	conflicting.To = fourth.Name
	conflicting.RequiredContracts = []ContractDependency{{c.ID, 2}}
	_, err = s.Send(workerCtx, worker.ID, conflicting)
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.UpdateContract(t.Context(), from.ID, c.ID, 1, c.Spec)
	require.NoError(t, err)
	effect := false
	grandCtx := withExecutionLineage(t.Context(), grand, fourthLease)
	require.ErrorIs(t, s.WithOwnedPaths(grandCtx, fourth.ID, []string{"out.txt"}, func(context.Context) error { effect = true; return nil }), ErrRequestContracts)
	require.False(t, effect)
	fresh := request("after-change")
	fresh.To = worker.Name
	_, err = s.Send(backCtx, back.ID, fresh)
	require.ErrorIs(t, err, ErrRequestContracts)
	rootNow, err := s.Read(t.Context(), back.ID, root.ID)
	require.NoError(t, err)
	require.Equal(t, Failed, rootNow.State)
	// A stored causal receipt remains retrievable without reaccepting work or advancing its pins.
	receipt, err := s.Send(backCtx, back.ID, childInput)
	require.NoError(t, err)
	require.Equal(t, child.ID, receipt.ID)
	require.Equal(t, root.RequiredContracts, receipt.RequiredContracts)
}
func TestRequestContractsNotificationsInheritButCannotDeclareRequirements(t *testing.T) {
	s, from, back, _ := fixture(t)
	c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "gate"))
	require.NoError(t, err)
	_, err = s.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
	require.NoError(t, err)
	root := contractRequest(t, s, from, c, "root")
	lease, err := s.AcquireLease(t.Context(), back.ID, uuid.NewString(), time.Minute)
	require.NoError(t, err)
	root, err = s.ClaimLease(t.Context(), lease)
	require.NoError(t, err)
	ctx := withExecutionLineage(t.Context(), root, lease)
	in := request("notice")
	in.Kind = "progress"
	in.To = from.Name
	notice, err := s.Send(ctx, back.ID, in)
	require.NoError(t, err)
	require.Equal(t, root.RequiredContracts, notice.RequiredContracts)
	graph, err := s.Graph(t.Context(), from.ID, notice.ID, 100)
	require.NoError(t, err)
	require.Equal(t, "waiting", graph.Nodes[0].Readiness)
	require.Len(t, graph.Nodes[0].ContractBlockers, 1)
	_, err = s.Claim(t.Context(), from.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.AcknowledgeContract(t.Context(), from.ID, c.ID, 1)
	require.NoError(t, err)
	frontLease, err := s.AcquireLease(t.Context(), from.ID, uuid.NewString(), time.Minute)
	require.NoError(t, err)
	notice, err = s.ClaimLease(t.Context(), frontLease)
	require.NoError(t, err)
	_, err = s.UpdateContract(t.Context(), from.ID, c.ID, 1, c.Spec)
	require.NoError(t, err)
	frontCtx := withExecutionLineage(t.Context(), notice, frontLease)
	require.ErrorIs(t, s.WithOwnedPaths(frontCtx, from.ID, []string{"out.txt"}, func(context.Context) error { t.Fatal("notification escaped inherited agreement"); return nil }), ErrRequestContracts)
	_, err = s.CompleteLease(frontCtx, frontLease, notice.ID, json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrRequestContracts)
	in.IdempotencyKey = "forged"
	in.RequiredContracts = []ContractDependency{{c.ID, 2}}
	_, err = s.Send(ctx, back.ID, in)
	require.ErrorContains(t, err, "only requests")
}
