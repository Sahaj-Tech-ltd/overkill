package coordination

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestDispatcherRejectsCircularRequestsWithDurableEscalation(t *testing.T) {
	store, front, back, _ := fixture(t)
	bindFixture(t, store, front, back)
	root, err := store.Send(t.Context(), front.ID, request("root"))
	require.NoError(t, err)
	rejected := make(chan error, 2)
	executor := testExecutor{reserve: func(_ context.Context, binding Binding) (Execution, error) {
		return &testExecution{run: func(ctx context.Context, m Message) (json.RawMessage, error) {
			target := front.Name
			if binding.Peer.ID == front.ID {
				target = back.Name
			}
			_, err := store.Send(ctx, binding.Peer.ID, SendInput{To: target, Kind: "request", ConversationID: uuid.NewString(), TaskID: "reset-ancestry", Payload: json.RawMessage(`{}`), IdempotencyKey: "circular:" + m.ID})
			rejected <- err
			return json.RawMessage(`{"resolved":true}`), nil
		}}, nil
	}}
	startDispatcher(t, store, executor, nil)
	for range 2 {
		select {
		case err := <-rejected:
			require.ErrorIs(t, err, ErrRequestBudget)
		case <-time.After(4 * time.Second):
			t.Fatal("dispatcher did not enforce request ancestry")
		}
	}
	waitState(t, store, front.ID, root.ID, Completed)
	escalations, err := store.ReadEscalations(t.Context(), front.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, escalations, 2)
	require.Equal(t, root.ConversationID, escalations[0].ConversationID)
	require.Equal(t, root.ID, escalations[0].ParentMessageID)
	require.Equal(t, "request_cycle", escalations[0].Reason)
	require.Equal(t, Blocked, escalations[0].State)
	require.True(t, escalations[0].RequiresReview)
	var count int
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_messages`).Scan(&count))
	require.Equal(t, 2, count, "only request and one terminal answer admitted")
}

func claimLineage(t *testing.T, store *Store, peer Peer) (context.Context, Message, Lease) {
	t.Helper()
	lease, err := store.AcquireLease(t.Context(), peer.ID, uuid.NewString(), time.Minute)
	require.NoError(t, err)
	m, err := store.ClaimLease(t.Context(), lease)
	require.NoError(t, err)
	return withExecutionLineage(t.Context(), m, lease), m, lease
}

func TestRequestLineageDerivedAndFenced(t *testing.T) {
	store, front, back, _ := fixture(t)
	third, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: "third", Role: "third", Workspace: t.TempDir()})
	require.NoError(t, err)
	input := request("root")
	input.TaskID = "real-task"
	input.Deadline = time.Now().Add(time.Minute)
	root, err := store.Send(t.Context(), front.ID, input)
	require.NoError(t, err)
	ctx, parent, lease := claimLineage(t, store, back)
	childInput := SendInput{To: third.Name, Kind: "request", ConversationID: uuid.NewString(), TaskID: "fake-task", Deadline: time.Now().Add(time.Hour), Payload: json.RawMessage(`{}`), IdempotencyKey: "child"}
	child, err := store.Send(ctx, back.ID, childInput)
	require.NoError(t, err)
	require.Equal(t, root.ID, child.ParentMessageID)
	require.Equal(t, root.ConversationID, child.ConversationID)
	require.Equal(t, root.TaskID, child.TaskID)
	require.Equal(t, root.Deadline, child.Deadline)
	require.Equal(t, 2, child.RequestDepth)
	retry, err := store.Send(ctx, back.ID, childInput)
	require.NoError(t, err)
	require.Equal(t, child.ID, retry.ID)
	_, err = store.Send(ctx, front.ID, request("wrong-peer"))
	require.ErrorIs(t, err, ErrUnauthorized)
	_, err = store.Transition(t.Context(), back.ID, parent.ID, parent.Revision, "blocked", json.RawMessage(`{}`))
	require.NoError(t, err)
	childInput.IdempotencyKey = "after-block"
	_, err = store.Send(ctx, back.ID, childInput)
	require.ErrorIs(t, err, ErrConflict)
	require.NoError(t, store.ReleaseLease(t.Context(), lease))
	_, err = store.Send(ctx, back.ID, childInput)
	require.ErrorIs(t, err, ErrLeaseLost)
}

func TestRequestDepthBoundSurvivesStoreRestart(t *testing.T) {
	store, front, back, _ := fixture(t)
	peers := []Peer{front, back}
	for i := 2; i <= MaxRequestDepth+1; i++ {
		p, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: fmt.Sprintf("hop%d", i), Role: "hop", Workspace: t.TempDir()})
		require.NoError(t, err)
		peers = append(peers, p)
	}
	first, err := store.Send(t.Context(), front.ID, request("root"))
	require.NoError(t, err)
	require.Equal(t, 1, first.RequestDepth)
	for i := 1; i <= MaxRequestDepth; i++ {
		ctx, parent, lease := claimLineage(t, store, peers[i])
		restarted, err := NewStore(t.Context(), store.db)
		require.NoError(t, err)
		child, err := restarted.Send(ctx, peers[i].ID, SendInput{To: peers[i+1].Name, Kind: "request", Payload: json.RawMessage(`{}`), IdempotencyKey: fmt.Sprintf("hop%d", i)})
		if i == MaxRequestDepth {
			require.ErrorIs(t, err, ErrRequestBudget)
		} else {
			require.NoError(t, err)
			require.Equal(t, parent.ID, child.ParentMessageID)
			require.Equal(t, i+1, child.RequestDepth)
		}
		require.NoError(t, store.ReleaseLease(t.Context(), lease))
	}
	events, err := store.ReadEscalations(t.Context(), peers[MaxRequestDepth].ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "request_depth_limit", events[0].Reason)
}

func TestConversationRequestBudgetAndIdempotency(t *testing.T) {
	store, front, _, _ := fixture(t)
	conversation := uuid.NewString()
	var first Message
	for i := range MaxConversationRequests {
		input := request(fmt.Sprintf("r%d", i))
		input.ConversationID = conversation
		m, err := store.Send(t.Context(), front.ID, input)
		require.NoError(t, err)
		if i == 0 {
			first = m
		}
	}
	input := request("over-budget")
	input.ConversationID = conversation
	_, err := store.Send(t.Context(), front.ID, input)
	require.ErrorIs(t, err, ErrRequestBudget)
	input = request("r0")
	input.ConversationID = conversation
	retry, err := store.Send(t.Context(), front.ID, input)
	require.NoError(t, err)
	require.Equal(t, first.ID, retry.ID)
	events, err := store.ReadEscalations(t.Context(), front.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "conversation_request_limit", events[0].Reason)
}

func TestConcurrentPeerQueueAdmissionAndDurableEscalationScope(t *testing.T) {
	store, front, back, _ := fixture(t)
	var first Message
	for i := range MaxPeerQueue - 1 {
		m, err := store.Send(t.Context(), front.ID, request(fmt.Sprintf("fill%d", i)))
		require.NoError(t, err)
		if i == 0 {
			first = m
		}
	}
	const racing = 12
	results := make(chan error, racing)
	var workers sync.WaitGroup
	for i := range racing {
		workers.Go(func() { _, err := store.Send(t.Context(), front.ID, request(fmt.Sprintf("race%d", i))); results <- err })
	}
	workers.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			require.ErrorIs(t, err, ErrQueueFull)
		}
	}
	require.Equal(t, 1, accepted)
	retry, err := store.Send(t.Context(), front.ID, request("fill0"))
	require.NoError(t, err)
	require.Equal(t, first.ID, retry.ID)
	restarted, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	escalations, err := restarted.ReadEscalations(t.Context(), back.ID, 0, 100)
	require.NoError(t, err)
	require.Len(t, escalations, racing-1)
	for _, e := range escalations {
		require.Equal(t, "peer_queue_limit", e.Reason)
		require.Equal(t, front.ID, e.From)
		require.Equal(t, back.ID, e.To)
	}
	page, err := store.ReadEscalations(t.Context(), front.ID, escalations[0].Sequence, 100)
	require.NoError(t, err)
	require.Len(t, page, racing-2)
	outsider, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: "other", Name: "outsider", Role: "other", Workspace: t.TempDir()})
	require.NoError(t, err)
	hidden, err := store.ReadEscalations(t.Context(), outsider.ID, 0, 10)
	require.NoError(t, err)
	require.Empty(t, hidden)
	require.NoError(t, store.DisablePeer(t.Context(), front.ID))
	hidden, err = store.ReadEscalations(t.Context(), front.ID, 0, 10)
	require.NoError(t, err)
	require.Empty(t, hidden)
}

func TestProjectQueueBoundAcrossRecipients(t *testing.T) {
	store, front, back, _ := fixture(t)
	recipients := []Peer{back}
	for i := range 4 {
		p, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: fmt.Sprintf("target%d", i), Role: "target", Workspace: t.TempDir()})
		require.NoError(t, err)
		recipients = append(recipients, p)
	}
	for i := range MaxProjectQueue {
		input := request(fmt.Sprintf("project%d", i))
		input.To = recipients[i%len(recipients)].Name
		_, err := store.Send(t.Context(), front.ID, input)
		require.NoError(t, err)
	}
	_, err := store.Send(t.Context(), front.ID, request("project-full"))
	require.ErrorIs(t, err, ErrQueueFull)
	events, err := store.ReadEscalations(t.Context(), front.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "project_queue_limit", events[0].Reason)
}

func TestTerminalAnswerReserveCannotBeReused(t *testing.T) {
	store, front, back, _ := fixture(t)
	for i := range MaxPeerQueue {
		_, err := store.Send(t.Context(), back.ID, SendInput{To: front.Name, Kind: "progress", Payload: json.RawMessage(`{}`), IdempotencyKey: fmt.Sprintf("fill%d", i)})
		require.NoError(t, err)
	}
	root, err := store.Send(t.Context(), front.ID, request("root"))
	require.NoError(t, err)
	answer, err := store.Reply(t.Context(), back.ID, root.ID, json.RawMessage(`{"ready":true}`), "answer")
	require.NoError(t, err)
	retry, err := store.Reply(t.Context(), back.ID, root.ID, json.RawMessage(`{"ready":true}`), "answer")
	require.NoError(t, err)
	require.Equal(t, answer.ID, retry.ID)
	_, err = store.Reply(t.Context(), back.ID, root.ID, json.RawMessage(`{}`), "another-answer")
	require.ErrorIs(t, err, ErrConflict)
	original, err := store.Read(t.Context(), front.ID, root.ID)
	require.NoError(t, err)
	require.Equal(t, Completed, original.State)
	var pending int
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_messages WHERE state IN ('accepted','started','blocked')`).Scan(&pending))
	require.Equal(t, MaxPeerQueue+1, pending)
}

func TestExecutionAttemptBudgetRequiresDurableReview(t *testing.T) {
	store, front, back, _ := fixture(t)
	root, err := store.Send(t.Context(), front.ID, request("root"))
	require.NoError(t, err)
	m, err := store.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, 1, m.ExecutionAttempts)
	for attempt := 1; attempt <= MaxExecutionAttempts; attempt++ {
		m, err = store.Transition(t.Context(), back.ID, m.ID, m.Revision, "blocked", json.RawMessage(`{}`))
		require.NoError(t, err)
		restarted, openErr := NewStore(t.Context(), store.db)
		require.NoError(t, openErr)
		resumed, resumeErr := restarted.Transition(t.Context(), back.ID, m.ID, m.Revision, "started", json.RawMessage(`{}`))
		if attempt == MaxExecutionAttempts {
			require.ErrorIs(t, resumeErr, ErrRequestBudget)
		} else {
			require.NoError(t, resumeErr)
			m = resumed
			require.Equal(t, attempt+1, m.ExecutionAttempts)
		}
	}
	m, err = store.Read(t.Context(), front.ID, root.ID)
	require.NoError(t, err)
	require.Equal(t, Blocked, m.State)
	require.Equal(t, MaxExecutionAttempts, m.ExecutionAttempts)
	events, err := store.MessageEvents(t.Context(), front.ID, m.ID, 0, 30)
	require.NoError(t, err)
	require.JSONEq(t, `{"reason":"execution_attempt_limit","requires_review":true,"limit":3}`, string(events[len(events)-1].Payload))
	escalations, err := store.ReadEscalations(t.Context(), front.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, escalations, 1)
	require.Equal(t, "execution_attempt_limit", escalations[0].Reason)
}
