package acp

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func taskFixture(t *testing.T) (*TaskStore, *sql.DB) {
	t.Helper()
	db, _ := testpg.Open(t)
	_, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO sessions(id) VALUES('one'),('two')`)
	require.NoError(t, err)
	store := NewTaskStore(db)
	require.NoError(t, store.Migrate(t.Context()))
	return store, db
}
func submitTask(t *testing.T, s *TaskStore, key, sid, content string) Task {
	t.Helper()
	task, err := s.Submit(t.Context(), TaskSubmitRequest{IdempotencyKey: key, SessionID: sid, Content: content, From: "test peer"})
	require.NoError(t, err)
	return task
}
func TestTaskStoreConcurrentIdempotencyAndConflict(t *testing.T) {
	store, db := taskFixture(t)
	start := make(chan struct{})
	results := make(chan Task, 16)
	failures := make(chan error, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			task, err := NewTaskStore(db).Submit(t.Context(), TaskSubmitRequest{IdempotencyKey: "same", SessionID: "one", Content: "once", From: "peer"})
			results <- task
			failures <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	id := ""
	for task := range results {
		if id == "" {
			id = task.TaskID
		}
		require.Equal(t, id, task.TaskID)
		require.Equal(t, "queued", task.Status)
	}
	for _, req := range []TaskSubmitRequest{{IdempotencyKey: "same", SessionID: "two", Content: "once", From: "peer"}, {IdempotencyKey: "same", SessionID: "one", Content: "changed", From: "peer"}, {IdempotencyKey: "same", SessionID: "one", Content: "once", From: "changed"}} {
		_, err := store.Submit(t.Context(), req)
		require.ErrorIs(t, err, ErrTaskConflict)
	}
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM acp_tasks`).Scan(&count))
	require.Equal(t, 1, count)
	reopened := NewTaskStore(db)
	require.NoError(t, reopened.Migrate(t.Context()))
	task, err := reopened.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, id, task.TaskID)
}
func TestTaskStoreValidationCapacityAndCancel(t *testing.T) {
	store, db := taskFixture(t)
	for _, req := range []TaskSubmitRequest{{Content: "x", SessionID: "one"}, {IdempotencyKey: "k", Content: "x"}, {IdempotencyKey: "k", SessionID: "one"}, {IdempotencyKey: "k", SessionID: "absent", Content: "x"}} {
		_, err := store.Submit(t.Context(), req)
		require.ErrorIs(t, err, ErrTaskInvalid)
	}
	first := submitTask(t, store, "0", "one", "cancel queued")
	for i := 1; i < TaskMaxActive; i++ {
		submitTask(t, store, fmt.Sprint(i), "one", "queued")
	}
	_, err := store.Submit(t.Context(), TaskSubmitRequest{IdempotencyKey: "overflow", SessionID: "one", Content: "x"})
	require.ErrorIs(t, err, ErrTaskCapacity)
	cancelled, err := store.Cancel(t.Context(), first.TaskID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", cancelled.Status)
	repeat := submitTask(t, store, "0", "one", "cancel queued")
	require.Equal(t, first.TaskID, repeat.TaskID)
	require.Equal(t, "cancelled", repeat.Status)
	submitTask(t, store, "replacement", "one", "space freed")
	_, err = store.Get(t.Context(), "absent")
	require.ErrorIs(t, err, ErrTaskNotFound)
	_, err = store.Cancel(t.Context(), "absent")
	require.ErrorIs(t, err, ErrTaskNotFound)
	var persisted string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT content FROM acp_tasks WHERE task_id=$1`, first.TaskID).Scan(&persisted))
	require.Equal(t, "cancel queued", persisted)
}
func TestTaskStoreAwaitDoesNotCancelQueuedTask(t *testing.T) {
	store, _ := taskFixture(t)
	task := submitTask(t, store, "wait", "one", "still queued")
	got, err := store.Await(t.Context(), task.TaskID, 100*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, "queued", got.Status)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = store.Await(ctx, task.TaskID, time.Second)
	require.Error(t, err)
	got, err = store.Get(t.Context(), task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "queued", got.Status)
	_, err = store.Await(t.Context(), task.TaskID, TaskMaxAwait+time.Second)
	require.ErrorIs(t, err, ErrTaskInvalid)
}
func TestTaskStoreNilAndMigrationRollback(t *testing.T) {
	for _, store := range []*TaskStore{nil, NewTaskStore(nil)} {
		require.ErrorIs(t, store.Migrate(t.Context()), ErrTaskUnavailable)
		_, err := store.Get(t.Context(), "id")
		require.ErrorIs(t, err, ErrTaskUnavailable)
	}
	db, _ := testpg.Open(t)
	store := NewTaskStore(db)
	require.Error(t, store.Migrate(t.Context()))
	var present bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass('acp_tasks') IS NOT NULL`).Scan(&present))
	require.False(t, present)
	_, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(t.Context()))
}

func TestTaskReceiptsSurviveSessionDeletion(t *testing.T) {
	store, db := taskFixture(t)
	completed := submitTask(t, store, "retained receipt", "one", "original effect")
	_, err := db.ExecContext(t.Context(), `UPDATE acp_tasks SET status='completed',reply='durable completion' WHERE task_id=$1`, completed.TaskID)
	require.NoError(t, err)
	queued := submitTask(t, store, "queued deleted", "one", "never execute")
	_, err = db.ExecContext(t.Context(), `DELETE FROM sessions WHERE id='one'`)
	require.NoError(t, err)
	result, err := store.Get(t.Context(), completed.TaskID)
	require.NoError(t, err)
	require.Equal(t, "completed", result.Status)
	require.Equal(t, "durable completion", result.Reply)
	repeat := submitTask(t, store, "retained receipt", "one", "original effect")
	require.Equal(t, completed.TaskID, repeat.TaskID)
	require.Equal(t, "durable completion", repeat.Reply)
	_, err = store.Submit(t.Context(), TaskSubmitRequest{IdempotencyKey: "retained receipt", SessionID: "one", Content: "different effect", From: "test peer"})
	require.ErrorIs(t, err, ErrTaskConflict)
	_, err = store.Submit(t.Context(), TaskSubmitRequest{IdempotencyKey: "new key deleted session", SessionID: "one", Content: "reject"})
	require.ErrorIs(t, err, ErrTaskInvalid)
	// Recreating the same text ID must not reconnect a previously severed
	// execution binding during migration/restart.
	_, err = db.ExecContext(t.Context(), `INSERT INTO sessions(id) VALUES('one')`)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(t.Context()))
	var reference sql.NullString
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT session_ref FROM acp_tasks WHERE task_id=$1`, queued.TaskID).Scan(&reference))
	require.False(t, reference.Valid)
	var calls atomic.Int32
	_, _ = startTaskTestServer(t, store, func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls}, nil
	})
	failed := waitTask(t, store, queued.TaskID, "failed")
	require.Contains(t, failed.Error, "deleted")
	require.Zero(t, calls.Load())
	require.NoError(t, store.Migrate(t.Context()))
	repeat = submitTask(t, store, "retained receipt", "one", "original effect")
	require.Equal(t, completed.TaskID, repeat.TaskID)
}

func TestTaskMigrationRepairsCandidateCascadingSessionBinding(t *testing.T) {
	store, db := taskFixture(t)
	task := submitTask(t, store, "old candidate receipt", "one", "preserve receipt")
	// Reproduce the initial candidate schema before the receipt-retention fix.
	_, err := db.ExecContext(t.Context(), `ALTER TABLE acp_tasks DROP COLUMN session_ref; ALTER TABLE acp_tasks ADD CONSTRAINT acp_tasks_session_id_fkey FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE`)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(t.Context()))
	_, err = db.ExecContext(t.Context(), `DELETE FROM sessions WHERE id='one'`)
	require.NoError(t, err)
	retained, err := store.Get(t.Context(), task.TaskID)
	require.NoError(t, err)
	require.Equal(t, task.TaskID, retained.TaskID)
	repeat := submitTask(t, store, "old candidate receipt", "one", "preserve receipt")
	require.Equal(t, task.TaskID, repeat.TaskID)
}
