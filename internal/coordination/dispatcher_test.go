package coordination

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

type testExecutor struct {
	reserve func(context.Context, Binding) (Execution, error)
}

func (e testExecutor) Reserve(ctx context.Context, binding Binding) (Execution, error) {
	return e.reserve(ctx, binding)
}

type testExecution struct {
	run     func(context.Context, Message) (json.RawMessage, error)
	release func()
}

func (e *testExecution) Run(ctx context.Context, m Message) (json.RawMessage, error) {
	return e.run(ctx, m)
}
func (e *testExecution) Release() {
	if e.release != nil {
		e.release()
	}
}

func bindFixture(t *testing.T, store *Store, peers ...Peer) {
	t.Helper()
	sessions, err := session.NewPostgresStore(store.db)
	require.NoError(t, err)
	for _, peer := range peers {
		sess := session.NewSession(peer.Workspace)
		require.NoError(t, sessions.Create(t.Context(), sess))
		require.NoError(t, store.BindSession(t.Context(), peer.ID, sess.ID))
	}
}

func startDispatcher(t *testing.T, store *Store, executor Executor, onError func(string, error)) (context.CancelFunc, <-chan error) {
	return startDispatcherTTL(t, store, executor, onError, 900*time.Millisecond)
}

func startDispatcherTTL(t *testing.T, store *Store, executor Executor, onError func(string, error), ttl time.Duration) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	d := &Dispatcher{Store: store, Executor: executor, Options: DispatchOptions{PollInterval: 20 * time.Millisecond, LeaseTTL: ttl, Concurrency: 4, OnError: onError}}
	go func() { done <- d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("dispatcher did not release owned workers")
		}
	})
	return cancel, done
}

func waitState(t *testing.T, store *Store, peerID, messageID string, state State) Message {
	t.Helper()
	var message Message
	require.Eventually(t, func() bool {
		m, err := store.Read(t.Context(), peerID, messageID)
		if err != nil {
			return false
		}
		message = m
		return m.State == state
	}, 4*time.Second, 10*time.Millisecond)
	return message
}

func TestDispatcherDeliversRequestAndAnswerWithoutReplyCycle(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	bindFixture(t, store, frontend, backend)
	message, err := store.Send(t.Context(), frontend.ID, request("schema"))
	require.NoError(t, err)
	notified := make(chan Message, 1)
	var runs atomic.Int32
	executor := testExecutor{reserve: func(ctx context.Context, binding Binding) (Execution, error) {
		return &testExecution{run: func(ctx context.Context, m Message) (json.RawMessage, error) {
			runs.Add(1)
			if binding.Peer.ID == backend.ID {
				return json.RawMessage(`{"schema":"users[]"}`), nil
			}
			notified <- m
			return json.RawMessage(`{"applied":true}`), nil
		}}, nil
	}}
	startDispatcher(t, store, executor, nil)
	var answer Message
	select {
	case answer = <-notified:
	case <-time.After(4 * time.Second):
		t.Fatal("frontend did not wake for correlated answer")
	}
	require.Equal(t, "answer", answer.Kind)
	require.Equal(t, message.ID, answer.InReplyTo)
	require.Equal(t, message.ConversationID, answer.ConversationID)
	require.JSONEq(t, `{"schema":"users[]"}`, string(answer.Payload))
	waitState(t, store, frontend.ID, message.ID, Completed)
	waitState(t, store, frontend.ID, answer.ID, Completed)
	require.Equal(t, int32(2), runs.Load())
	events, err := store.Events(t.Context(), frontend.ID, 0, 50)
	require.NoError(t, err)
	require.Len(t, events, 6, "request and answer finish once; no acknowledgement loop")
}

func TestDispatcherLeavesBusySessionUnclaimed(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	bindFixture(t, store, backend)
	message, err := store.Send(t.Context(), frontend.ID, request("queued"))
	require.NoError(t, err)
	var busy atomic.Bool
	busy.Store(true)
	attempts := make(chan struct{}, 20)
	executor := testExecutor{reserve: func(ctx context.Context, binding Binding) (Execution, error) {
		if busy.Load() {
			select {
			case attempts <- struct{}{}:
			default:
			}
			return nil, ErrBusy
		}
		return &testExecution{run: func(context.Context, Message) (json.RawMessage, error) { return json.RawMessage(`{"ready":true}`), nil }}, nil
	}}
	startDispatcher(t, store, executor, nil)
	for range 3 {
		select {
		case <-attempts:
		case <-time.After(3 * time.Second):
			t.Fatal("busy reservation was not retried")
		}
	}
	queued, err := store.Read(t.Context(), frontend.ID, message.ID)
	require.NoError(t, err)
	require.Equal(t, Accepted, queued.State)
	busy.Store(false)
	waitState(t, store, frontend.ID, message.ID, Completed)
}

func TestCompetingDispatchersKeepOneExecutionWhileLeaseRenews(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	bindFixture(t, store, backend)
	message, err := store.Send(t.Context(), frontend.ID, request("single"))
	require.NoError(t, err)
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	var count atomic.Int32
	executor := testExecutor{reserve: func(context.Context, Binding) (Execution, error) {
		return &testExecution{run: func(ctx context.Context, m Message) (json.RawMessage, error) {
			count.Add(1)
			started <- struct{}{}
			select {
			case <-release:
				return json.RawMessage(`{"once":true}`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}, nil
	}}
	startDispatcher(t, store, executor, nil)
	startDispatcher(t, store, executor, nil)
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("executor did not start")
	}
	var first time.Time
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT expires_at FROM coordination_leases WHERE peer_id=$1`, backend.ID).Scan(&first))
	require.Eventually(t, func() bool {
		var now time.Time
		err := store.db.QueryRowContext(t.Context(), `SELECT expires_at FROM coordination_leases WHERE peer_id=$1`, backend.ID).Scan(&now)
		return err == nil && now.After(first.Add(900*time.Millisecond))
	}, 4*time.Second, 20*time.Millisecond)
	require.Equal(t, int32(1), count.Load())
	close(release)
	waitState(t, store, frontend.ID, message.ID, Completed)
	require.Equal(t, int32(1), count.Load())
}

func TestSuccessfulExecutionWhileHeartbeatQueryIsCancelled(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	bindFixture(t, store, backend)
	message, err := store.Send(t.Context(), frontend.ID, request("heartbeat cleanup"))
	require.NoError(t, err)
	finish := make(chan struct{})
	executor := testExecutor{reserve: func(context.Context, Binding) (Execution, error) {
		return &testExecution{run: func(ctx context.Context, _ Message) (json.RawMessage, error) {
			select {
			case <-finish:
				return json.RawMessage(`{"completed":true}`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}, nil
	}}
	startDispatcherTTL(t, store, executor, nil, 3*time.Second)
	waitState(t, store, frontend.ID, message.ID, Started)
	// Hold the lease row so the next renewal is provably in flight. Finishing
	// the executor then cancels that query as part of normal heartbeat cleanup.
	blocker, err := store.db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer blocker.Rollback()
	var owner string
	require.NoError(t, blocker.QueryRowContext(t.Context(), `SELECT owner_id FROM coordination_leases WHERE peer_id=$1 FOR UPDATE`, backend.ID).Scan(&owner))
	var blockerPID int
	require.NoError(t, blocker.QueryRowContext(t.Context(), `SELECT pg_backend_pid()`).Scan(&blockerPID))
	waiting := func() bool {
		var blocked bool
		err := store.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE 'UPDATE coordination_leases%')`, blockerPID).Scan(&blocked)
		require.NoError(t, err)
		return blocked
	}
	require.Eventually(t, waiting, 2*time.Second, 10*time.Millisecond)
	close(finish)
	require.Eventually(t, func() bool { return !waiting() }, time.Second, 10*time.Millisecond)
	require.NoError(t, blocker.Rollback())
	waitState(t, store, frontend.ID, message.ID, Completed)
}

func TestDispatcherCancellationStopsOwnedExecution(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	bindFixture(t, store, backend)
	message, err := store.Send(t.Context(), frontend.ID, request("cancel"))
	require.NoError(t, err)
	stopped := make(chan struct{})
	executor := testExecutor{reserve: func(context.Context, Binding) (Execution, error) {
		return &testExecution{run: func(ctx context.Context, _ Message) (json.RawMessage, error) {
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		}}, nil
	}}
	startDispatcher(t, store, executor, nil)
	started := waitState(t, store, frontend.ID, message.ID, Started)
	_, err = store.Transition(t.Context(), frontend.ID, message.ID, started.Revision, "cancelled", json.RawMessage(`{}`))
	require.NoError(t, err)
	select {
	case <-stopped:
	case <-time.After(4 * time.Second):
		t.Fatal("cancelled execution still running")
	}
	waitState(t, store, frontend.ID, message.ID, Cancelled)
}

func TestDispatcherPanicsFailDurablyAndReleaseReservation(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	bindFixture(t, store, backend)
	message, err := store.Send(t.Context(), frontend.ID, request("panic"))
	require.NoError(t, err)
	var released atomic.Int32
	executor := testExecutor{reserve: func(context.Context, Binding) (Execution, error) {
		return &testExecution{run: func(context.Context, Message) (json.RawMessage, error) { panic("fixture executor") }, release: func() { released.Add(1) }}, nil
	}}
	startDispatcher(t, store, executor, nil)
	waitState(t, store, frontend.ID, message.ID, Failed)
	require.Eventually(t, func() bool { return released.Load() == 1 }, time.Second, 5*time.Millisecond)
	events, err := store.MessageEvents(t.Context(), frontend.ID, message.ID, 0, 10)
	require.NoError(t, err)
	require.Contains(t, string(events[len(events)-1].Payload), "fixture executor")
}

func TestDispatcherExpiresQueuedMessageWithoutExecuting(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	bindFixture(t, store, backend)
	message, err := store.Send(t.Context(), frontend.ID, request("expired"))
	require.NoError(t, err)
	_, err = store.db.ExecContext(t.Context(), `UPDATE coordination_messages SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, message.ID)
	require.NoError(t, err)
	var count atomic.Int32
	executor := testExecutor{reserve: func(context.Context, Binding) (Execution, error) {
		return &testExecution{run: func(context.Context, Message) (json.RawMessage, error) {
			count.Add(1)
			return json.RawMessage(`{}`), nil
		}}, nil
	}}
	startDispatcher(t, store, executor, nil)
	waitState(t, store, frontend.ID, message.ID, Failed)
	require.Zero(t, count.Load())
}

func TestDispatcherEnforcesConfiguredGlobalConcurrency(t *testing.T) {
	store, front, _, _ := fixture(t)
	var targets []Peer
	for i := range 5 {
		p, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: fmt.Sprintf("worker-%d", i), Role: "worker", Workspace: t.TempDir()})
		require.NoError(t, err)
		targets = append(targets, p)
		_, err = store.Send(t.Context(), front.ID, SendInput{To: p.Name, Kind: "request", Payload: json.RawMessage(`{}`), IdempotencyKey: p.Name})
		require.NoError(t, err)
	}
	bindFixture(t, store, targets...)
	var active, maximum, executed atomic.Int32
	started := make(chan struct{}, 5)
	release := make(chan struct{})
	executor := testExecutor{reserve: func(_ context.Context, b Binding) (Execution, error) {
		return &testExecution{run: func(ctx context.Context, m Message) (json.RawMessage, error) {
			now := active.Add(1)
			defer active.Add(-1)
			for {
				old := maximum.Load()
				if now <= old || maximum.CompareAndSwap(old, now) {
					break
				}
			}
			executed.Add(1)
			started <- struct{}{}
			select {
			case <-release:
				return json.RawMessage(`{}`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}}, nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- (&Dispatcher{Store: store, Executor: executor, Options: DispatchOptions{Concurrency: 2, PollInterval: 10 * time.Millisecond, LeaseTTL: time.Second}}).Run(ctx)
	}()
	defer func() { cancel(); require.NoError(t, <-done) }()
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("configured workers did not start")
		}
	}
	require.Equal(t, int32(2), active.Load())
	require.Equal(t, int32(2), maximum.Load())
	select {
	case <-started:
		t.Fatal("dispatcher exceeded configured concurrency while slots held")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	require.Eventually(t, func() bool { return executed.Load() == 5 }, 4*time.Second, 10*time.Millisecond)
	require.LessOrEqual(t, maximum.Load(), int32(2))
}
