package acp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type taskTestSender struct {
	id      string
	calls   *atomic.Int32
	release <-chan struct{}
	fail    bool
	output  string
}

func (s *taskTestSender) Model() string     { return "must-not-be-persisted" }
func (s *taskTestSender) SessionID() string { return s.id }
func (s *taskTestSender) StreamACP(ctx context.Context, input string) (<-chan AgentEvent, error) {
	s.calls.Add(1)
	events := make(chan AgentEvent)
	go func() {
		defer close(events)
		select {
		case events <- AgentEvent{Type: AgentEventToken, Content: s.output}:
		case <-ctx.Done():
			return
		}
		if s.release != nil {
			select {
			case <-s.release:
			case <-ctx.Done():
				return
			}
		}
		if s.fail {
			select {
			case events <- AgentEvent{Type: AgentEventError, Error: fmt.Errorf("secret provider token")}:
			case <-ctx.Done():
			}
		} else {
			select {
			case events <- AgentEvent{Type: AgentEventDone}:
			case <-ctx.Done():
			}
		}
	}()
	return events, nil
}
func startTaskTestServer(t *testing.T, store *TaskStore, resolve func(context.Context, string) (Sender, error)) (*Server, *httptest.Server) {
	t.Helper()
	server := NewServer(Config{Token: "credential-do-not-persist", Tasks: store, ResolveSender: resolve})
	require.NoError(t, server.StartTasks(t.Context()))
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
	})
	return server, httpServer
}
func taskHTTP(t *testing.T, url, method, path, body, token string) (int, Task, string) {
	t.Helper()
	req, err := http.NewRequest(method, url+path, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	var raw json.RawMessage
	require.NoError(t, json.NewDecoder(response.Body).Decode(&raw))
	var task Task
	_ = json.Unmarshal(raw, &task)
	return response.StatusCode, task, string(raw)
}
func waitTask(t *testing.T, store *TaskStore, id, status string) Task {
	t.Helper()
	var got Task
	require.Eventually(t, func() bool {
		var err error
		got, err = store.Get(t.Context(), id)
		return err == nil && got.Status == status
	}, 5*time.Second, 20*time.Millisecond)
	return got
}
func TestTaskServerDurableReplyAuthAndClientDisconnect(t *testing.T) {
	store, db := taskFixture(t)
	var calls atomic.Int32
	release := make(chan struct{})
	_, httpServer := startTaskTestServer(t, store, func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, release: release, output: "durable reply"}, nil
	})
	body := `{"idempotencyKey":"external-1","sessionID":"one","content":"do once","from":"display label"}`
	status, _, _ := taskHTTP(t, httpServer.URL, "POST", "/v1/tasks", body, "")
	require.Equal(t, http.StatusUnauthorized, status)
	status, task, _ := taskHTTP(t, httpServer.URL, "POST", "/v1/tasks", body, "credential-do-not-persist")
	require.Equal(t, http.StatusAccepted, status)
	require.NotEmpty(t, task.TaskID)
	require.NotEmpty(t, task.MessageID)
	require.Equal(t, "one", task.SessionID)
	waitTask(t, store, task.TaskID, "running")
	status, waiting, _ := taskHTTP(t, httpServer.URL, "GET", "/v1/tasks/"+task.TaskID+"/await?timeoutMs=0", "", "credential-do-not-persist")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "running", waiting.Status)
	// Submit's HTTP request is already gone; execution remains owned by server.
	close(release)
	completed := waitTask(t, store, task.TaskID, "completed")
	require.Equal(t, "durable reply", completed.Reply)
	require.Equal(t, int32(1), calls.Load())
	status, repeated, _ := taskHTTP(t, httpServer.URL, "POST", "/v1/tasks", body, "credential-do-not-persist")
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, task.TaskID, repeated.TaskID)
	require.Equal(t, "completed", repeated.Status)
	status, _, _ = taskHTTP(t, httpServer.URL, "POST", "/v1/tasks", strings.Replace(body, "do once", "different", 1), "credential-do-not-persist")
	require.Equal(t, http.StatusConflict, status)
	persisted, err := NewTaskStore(db).Get(t.Context(), task.TaskID)
	require.NoError(t, err)
	require.Equal(t, completed, persisted)
	var row string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT row_to_json(acp_tasks)::text FROM acp_tasks WHERE task_id=$1`, task.TaskID).Scan(&row))
	require.NotContains(t, row, "credential-do-not-persist")
	require.NotContains(t, row, "must-not-be-persisted")
	for _, path := range []string{"/v1/tasks/" + task.TaskID, "/v1/tasks/" + task.TaskID + "/await", "/v1/tasks/" + task.TaskID + "/cancel"} {
		method := "GET"
		if strings.HasSuffix(path, "cancel") {
			method = "POST"
		}
		status, _, _ = taskHTTP(t, httpServer.URL, method, path, "", "")
		require.Equal(t, http.StatusUnauthorized, status)
	}
}
func TestTaskServerTwoServersClaimOnceAndPreserveLiveOwner(t *testing.T) {
	store, _ := taskFixture(t)
	var calls atomic.Int32
	release := make(chan struct{})
	resolve := func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, release: release, output: "once"}, nil
	}
	_, _ = startTaskTestServer(t, store, resolve)
	task := submitTask(t, store, "two servers", "one", "effect")
	waitTask(t, store, task.TaskID, "running")
	_, _ = startTaskTestServer(t, store, resolve)
	time.Sleep(350 * time.Millisecond)
	running, err := store.Get(t.Context(), task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "running", running.Status)
	require.Equal(t, int32(1), calls.Load())
	close(release)
	waitTask(t, store, task.TaskID, "completed")
	require.Equal(t, int32(1), calls.Load())
}
func TestTaskServerRecoverAbandonedRunningWithoutReplayAndDispatchQueued(t *testing.T) {
	store, db := taskFixture(t)
	var calls atomic.Int32
	abandoned := submitTask(t, store, "abandoned", "one", "do not replay")
	_, err := db.ExecContext(t.Context(), `UPDATE acp_tasks SET status='running',reply='confirmed partial reply' WHERE task_id=$1`, abandoned.TaskID)
	require.NoError(t, err)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	lease := &taskLease{conn: conn}
	locked, err := lease.lock(t.Context(), "task:"+abandoned.TaskID)
	require.NoError(t, err)
	require.True(t, locked)
	_, _ = startTaskTestServer(t, store, func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, output: "queued finished"}, nil
	})
	time.Sleep(300 * time.Millisecond)
	live, err := store.Get(t.Context(), abandoned.TaskID)
	require.NoError(t, err)
	require.Equal(t, "running", live.Status)
	lease.close()
	interrupted := waitTask(t, store, abandoned.TaskID, "interrupted")
	require.Equal(t, "confirmed partial reply", interrupted.Reply)
	require.Zero(t, calls.Load())
	queued := submitTask(t, store, "queued after restart", "two", "safe not started")
	waitTask(t, store, queued.TaskID, "completed")
	require.Equal(t, int32(1), calls.Load())
}
func TestTaskServerCancelAndShutdownPersistTerminalState(t *testing.T) {
	store, _ := taskFixture(t)
	var calls atomic.Int32
	release := make(chan struct{})
	server, httpServer := startTaskTestServer(t, store, func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, release: release, output: "partial"}, nil
	})
	task := submitTask(t, store, "cancel active", "one", "cancel me")
	waitTask(t, store, task.TaskID, "running")
	status, cancelled, _ := taskHTTP(t, httpServer.URL, "POST", "/v1/tasks/"+task.TaskID+"/cancel", "", "credential-do-not-persist")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "cancelled", cancelled.Status)
	next := submitTask(t, store, "shutdown active", "two", "interrupt me")
	waitTask(t, store, next.TaskID, "running")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, server.Shutdown(ctx))
	waitTask(t, store, next.TaskID, "interrupted")
	persisted, err := store.Get(t.Context(), task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", persisted.Status)
}
func TestTaskServerLegacyAndDurableSessionAdmission(t *testing.T) {
	store, _ := taskFixture(t)
	var calls atomic.Int32
	release := make(chan struct{})
	_, httpServer := startTaskTestServer(t, store, func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, release: release, output: "partial"}, nil
	})
	task := submitTask(t, store, "admission", "one", "durable holds session")
	waitTask(t, store, task.TaskID, "running")
	status, _, _ := taskHTTP(t, httpServer.URL, "POST", "/v1/messages", `{"sessionID":"one","content":"legacy overlap"}`, "credential-do-not-persist")
	require.Equal(t, http.StatusConflict, status)
	status, _, _ = taskHTTP(t, httpServer.URL, "POST", "/v1/messages", `{"sessionID":"two","content":"legacy first"}`, "credential-do-not-persist")
	require.Equal(t, http.StatusAccepted, status)
	blocked := submitTask(t, store, "legacy blocks durable", "two", "queued behind legacy")
	time.Sleep(250 * time.Millisecond)
	got, err := store.Get(t.Context(), blocked.TaskID)
	require.NoError(t, err)
	require.Equal(t, "queued", got.Status)
	require.Equal(t, int32(2), calls.Load())
	close(release)
	waitTask(t, store, task.TaskID, "completed")
	waitTask(t, store, blocked.TaskID, "completed")
	require.Equal(t, int32(3), calls.Load())
}
func TestTaskServerFailureAndStrictBodies(t *testing.T) {
	store, _ := taskFixture(t)
	var calls atomic.Int32
	_, httpServer := startTaskTestServer(t, store, func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, fail: true, output: "partial"}, nil
	})
	for _, body := range []string{`null`, `{}`, `{"idempotencyKey":"k","sessionID":"one","content":"x","content":"y"}`, `{"idempotencyKey":"k","sessionID":"one","Content":"x"}`, `{"idempotencyKey":"k","sessionID":"one","content":"x","options":{}}`, `{"idempotencyKey":"k","sessionID":"one","content":null}`, `{"idempotencyKey":"k","sessionID":"one","content":"x"} {}`} {
		status, _, _ := taskHTTP(t, httpServer.URL, "POST", "/v1/tasks", body, "credential-do-not-persist")
		require.Equal(t, http.StatusBadRequest, status, body)
	}
	task := submitTask(t, store, "failure", "one", "fail safely")
	failed := waitTask(t, store, task.TaskID, "failed")
	require.Contains(t, failed.Error, "execution error")
	require.NotContains(t, failed.Error, "secret provider token")
	require.Equal(t, "partial", failed.Reply)
	for _, query := range []string{"?timeoutMs=-1", "?timeoutMs=25001", "?timeoutMs=0&timeoutMs=1", "?unknown=1", "?timeoutMs=%GG"} {
		status, _, _ := taskHTTP(t, httpServer.URL, "GET", "/v1/tasks/"+task.TaskID+"/await"+query, "", "credential-do-not-persist")
		require.Equal(t, http.StatusBadRequest, status)
	}
}

func TestTaskServerConnectionLossInterruptsWithoutReplay(t *testing.T) {
	store, db := taskFixture(t)
	var calls atomic.Int32
	release := make(chan struct{})
	_, _ = startTaskTestServer(t, store, func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, release: release, output: "partial checkpoint"}, nil
	})
	task := submitTask(t, store, "lost connection", "one", "once only")
	require.Eventually(t, func() bool {
		got, err := store.Get(t.Context(), task.TaskID)
		return err == nil && got.Status == "running" && got.Reply == "partial checkpoint"
	}, 5*time.Second, 20*time.Millisecond)
	var killed bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype='advisory' AND classid=((hashtextextended(current_schema()||':overkill:acp:task:'||$1,0)>>32)&4294967295)::oid AND objid=(hashtextextended(current_schema()||':overkill:acp:task:'||$1,0)&4294967295)::oid AND objsubid=1 LIMIT 1`, task.TaskID).Scan(&killed))
	require.True(t, killed)
	interrupted := waitTask(t, store, task.TaskID, "interrupted")
	require.Equal(t, "partial checkpoint", interrupted.Reply)
	require.Equal(t, int32(1), calls.Load())
}
func TestTaskServerGlobalConcurrencyBoundAcrossServers(t *testing.T) {
	store, db := taskFixture(t)
	_, err := db.ExecContext(t.Context(), `INSERT INTO sessions(id) VALUES('three'),('four'),('five'),('six')`)
	require.NoError(t, err)
	var calls atomic.Int32
	release := make(chan struct{})
	resolve := func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, release: release, output: "done"}, nil
	}
	_, _ = startTaskTestServer(t, store, resolve)
	_, _ = startTaskTestServer(t, store, resolve)
	var tasks []Task
	for _, sid := range []string{"one", "two", "three", "four", "five", "six"} {
		tasks = append(tasks, submitTask(t, store, "capacity-"+sid, sid, "work"))
	}
	require.Eventually(t, func() bool { return calls.Load() == TaskConcurrency }, 5*time.Second, 20*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, int32(TaskConcurrency), calls.Load())
	var running int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM acp_tasks WHERE status='running'`).Scan(&running))
	require.Equal(t, TaskConcurrency, running)
	close(release)
	for _, task := range tasks {
		waitTask(t, store, task.TaskID, "completed")
	}
	require.Equal(t, int32(6), calls.Load())
}
func TestTaskServerRestartDispatchesOnlyQueued(t *testing.T) {
	store, db := taskFixture(t)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	lease := &taskLease{conn: conn}
	ok, err := lease.lock(t.Context(), "session:one")
	require.NoError(t, err)
	require.True(t, ok)
	var calls atomic.Int32
	resolve := func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, output: "after restart"}, nil
	}
	server, _ := startTaskTestServer(t, store, resolve)
	queued := submitTask(t, store, "restart queued", "one", "not started")
	time.Sleep(250 * time.Millisecond)
	before, err := store.Get(t.Context(), queued.TaskID)
	require.NoError(t, err)
	require.Equal(t, "queued", before.Status)
	require.Zero(t, calls.Load())
	require.NoError(t, server.Shutdown(t.Context()))
	lease.close()
	_, _ = startTaskTestServer(t, store, resolve)
	done := waitTask(t, store, queued.TaskID, "completed")
	require.Equal(t, "after restart", done.Reply)
	require.Equal(t, int32(1), calls.Load())
}

func TestTaskServerMountedHandlerRequiresExplicitWorkersAndReportsLifetimeEnd(t *testing.T) {
	store, _ := taskFixture(t)
	server := NewServer(Config{Token: "credential-do-not-persist", Tasks: store})
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	defer server.Shutdown(context.Background())
	body := `{"idempotencyKey":"before workers","sessionID":"one","content":"wait"}`
	status, _, _ := taskHTTP(t, httpServer.URL, "POST", "/v1/tasks", body, "credential-do-not-persist")
	require.Equal(t, http.StatusServiceUnavailable, status)
	task := submitTask(t, store, "mounted status", "one", "queued")
	status, current, _ := taskHTTP(t, httpServer.URL, "GET", "/v1/tasks/"+task.TaskID, "", "credential-do-not-persist")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, current.WorkerError, "not started")
	lifetime, cancel := context.WithCancel(t.Context())
	require.NoError(t, server.StartTasks(lifetime))
	cancel()
	require.Eventually(t, func() bool { server.mu.Lock(); defer server.mu.Unlock(); return server.tasksStopped }, 5*time.Second, 20*time.Millisecond)
	status, current, _ = taskHTTP(t, httpServer.URL, "GET", "/v1/tasks/"+task.TaskID, "", "credential-do-not-persist")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, current.WorkerError, "lifetime ended")
	status, _, _ = taskHTTP(t, httpServer.URL, "POST", "/v1/tasks", body, "credential-do-not-persist")
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.ErrorIs(t, server.StartTasks(t.Context()), ErrTaskUnavailable)
}

type taskProcessSender struct {
	db *sql.DB
	id string
}

func (s *taskProcessSender) Model() string     { return "scripted" }
func (s *taskProcessSender) SessionID() string { return s.id }
func (s *taskProcessSender) StreamACP(ctx context.Context, input string) (<-chan AgentEvent, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE task_process_effect SET calls=calls+1`); err != nil {
		return nil, err
	}
	ch := make(chan AgentEvent)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var release bool
				if err := s.db.QueryRowContext(ctx, `SELECT released FROM task_process_effect`).Scan(&release); err != nil {
					return
				}
				if release {
					select {
					case ch <- AgentEvent{Type: AgentEventToken, Content: "subprocess completion"}:
					case <-ctx.Done():
						return
					}
					select {
					case ch <- AgentEvent{Type: AgentEventDone}:
					case <-ctx.Done():
					}
					return
				}
			}
		}
	}()
	return ch, nil
}
func TestTaskServerSubprocessHelper(t *testing.T) {
	dsn := os.Getenv("OVERKILL_ACP_TASK_HELPER_DSN")
	if dsn == "" {
		return
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	store := NewTaskStore(db)
	require.NoError(t, store.Migrate(t.Context()))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	server := NewServer(Config{Token: "subprocess credential", Tasks: store, ResolveSender: func(ctx context.Context, sid string) (Sender, error) { return &taskProcessSender{db: db, id: sid}, nil }})
	require.NoError(t, server.StartTasks(ctx))
	defer server.Shutdown(context.Background())
	id := os.Getenv("OVERKILL_ACP_TASK_HELPER_ID")
	require.Eventually(t, func() bool { task, err := store.Get(ctx, id); return err == nil && task.Status == "completed" }, 8*time.Second, 40*time.Millisecond)
}
func TestTaskServerIndependentProcessesClaimOneEffect(t *testing.T) {
	store, db := taskFixture(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE task_process_effect(calls INTEGER NOT NULL,released BOOLEAN NOT NULL);INSERT INTO task_process_effect VALUES(0,false)`)
	require.NoError(t, err)
	task := submitTask(t, store, "independent processes", "one", "exactly one confirmed admission")
	var schema string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT current_schema()`).Scan(&schema))
	dsn := os.Getenv("PG_TEST_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var children []*exec.Cmd
	var logs []*bytes.Buffer
	for range 2 {
		command := exec.CommandContext(ctx, executable, "-test.run=^TestTaskServerSubprocessHelper$", "-test.count=1")
		command.Env = append(os.Environ(), "OVERKILL_ACP_TASK_HELPER_DSN="+dsn, "OVERKILL_ACP_TASK_HELPER_ID="+task.TaskID)
		var log bytes.Buffer
		command.Stdout = &log
		command.Stderr = &log
		require.NoError(t, command.Start())
		children = append(children, command)
		logs = append(logs, &log)
	}
	// Always reap both children before the private schema is cleaned up.
	defer func() {
		cancel()
		for _, child := range children {
			if child.ProcessState == nil {
				_ = child.Wait()
			}
		}
	}()
	var count int
	require.Eventually(t, func() bool {
		err := db.QueryRowContext(t.Context(), `SELECT calls FROM task_process_effect`).Scan(&count)
		return err == nil && count == 1
	}, 5*time.Second, 40*time.Millisecond)
	time.Sleep(350 * time.Millisecond)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT calls FROM task_process_effect`).Scan(&count))
	require.Equal(t, 1, count)
	_, err = db.ExecContext(t.Context(), `UPDATE task_process_effect SET released=true`)
	require.NoError(t, err)
	for i, child := range children {
		require.NoError(t, child.Wait(), "child%d: %s", i, logs[i].String())
	}
	done := waitTask(t, store, task.TaskID, "completed")
	require.Equal(t, "subprocess completion", done.Reply)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT calls FROM task_process_effect`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestTaskServerShutdownDuringBlockedCheckpointPersistsInterruption(t *testing.T) {
	store, db := taskFixture(t)
	holder, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer holder.Rollback()
	_, err = holder.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':test:blocked-task-checkpoint',0))`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `CREATE FUNCTION block_task_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='running' AND NEW.reply<>OLD.reply THEN PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':test:blocked-task-checkpoint',0)); END IF; RETURN NEW; END $$;CREATE TRIGGER block_task_checkpoint BEFORE UPDATE ON acp_tasks FOR EACH ROW EXECUTE FUNCTION block_task_checkpoint()`)
	require.NoError(t, err)
	var calls atomic.Int32
	release := make(chan struct{})
	server, _ := startTaskTestServer(t, store, func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, release: release, output: "uncommitted checkpoint"}, nil
	})
	task := submitTask(t, store, "blocked checkpoint shutdown", "one", "stop safely")
	require.Eventually(t, func() bool {
		var blocked bool
		err := db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid=((hashtextextended(current_schema()||':test:blocked-task-checkpoint',0)>>32)&4294967295)::oid AND objid=(hashtextextended(current_schema()||':test:blocked-task-checkpoint',0)&4294967295)::oid AND objsubid=1)`).Scan(&blocked)
		return err == nil && blocked
	}, 5*time.Second, 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, server.Shutdown(ctx))
	persisted, err := store.Get(t.Context(), task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", persisted.Status)
	require.Empty(t, persisted.Reply, "an uncommitted checkpoint is never claimed durable")
	require.Equal(t, int32(1), calls.Load())
}

func TestTaskFinalizationRepairPreservesTerminalAndRespectsLiveOwnership(t *testing.T) {
	store, db := taskFixture(t)
	cancelled := submitTask(t, store, "already cancelled repair", "one", "cancelled")
	_, err := db.ExecContext(t.Context(), `UPDATE acp_tasks SET status='cancelled',reply='confirmed checkpoint',error_msg='cancelled by caller' WHERE task_id=$1`, cancelled.TaskID)
	require.NoError(t, err)
	before, err := store.Get(t.Context(), cancelled.TaskID)
	require.NoError(t, err)
	require.NoError(t, store.interruptUnownedTask(t.Context(), cancelled.TaskID))
	after, err := store.Get(t.Context(), cancelled.TaskID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	active := submitTask(t, store, "live ownership repair", "two", "running")
	_, err = db.ExecContext(t.Context(), `UPDATE acp_tasks SET status='running',reply='earlier durable checkpoint' WHERE task_id=$1`, active.TaskID)
	require.NoError(t, err)
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	owner := &taskLease{conn: conn}
	defer owner.close()
	ok, err := owner.lock(t.Context(), "task:"+active.TaskID)
	require.NoError(t, err)
	require.True(t, ok)
	require.ErrorContains(t, store.interruptUnownedTask(t.Context(), active.TaskID), "ownership held")
	untouched, err := store.Get(t.Context(), active.TaskID)
	require.NoError(t, err)
	require.Equal(t, "running", untouched.Status)
	owner.close()
	owner = nil
	require.NoError(t, store.interruptUnownedTask(t.Context(), active.TaskID))
	interrupted, err := store.Get(t.Context(), active.TaskID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", interrupted.Status)
	require.Equal(t, "earlier durable checkpoint", interrupted.Reply)
	require.ErrorIs(t, store.interruptUnownedTask(t.Context(), "missing-task"), ErrTaskNotFound)
}

func TestTaskServerShutdownReportsUnconfirmedFinalization(t *testing.T) {
	store, db := taskFixture(t)
	holder, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer holder.Rollback()
	_, err = holder.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':test:failed-task-finalization',0))`)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `CREATE FUNCTION fail_task_finalization() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='interrupted' THEN RAISE EXCEPTION 'fixture refuses interruption persistence'; END IF; IF NEW.status='running' AND NEW.reply<>OLD.reply THEN PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':test:failed-task-finalization',0)); END IF; RETURN NEW; END $$;CREATE TRIGGER fail_task_finalization BEFORE UPDATE ON acp_tasks FOR EACH ROW EXECUTE FUNCTION fail_task_finalization()`)
	require.NoError(t, err)
	var calls atomic.Int32
	release := make(chan struct{})
	server := NewServer(Config{Token: "test credential", Tasks: store, ResolveSender: func(ctx context.Context, sid string) (Sender, error) {
		return &taskTestSender{id: sid, calls: &calls, release: release, output: "uncommitted"}, nil
	}})
	require.NoError(t, server.StartTasks(t.Context()))
	defer func() { _ = server.Shutdown(context.Background()) }()
	task := submitTask(t, store, "failed shutdown persistence", "one", "unconfirmed")
	require.Eventually(t, func() bool {
		var blocked bool
		err := db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid=((hashtextextended(current_schema()||':test:failed-task-finalization',0)>>32)&4294967295)::oid AND objid=(hashtextextended(current_schema()||':test:failed-task-finalization',0)&4294967295)::oid AND objsubid=1)`).Scan(&blocked)
		return err == nil && blocked
	}, 5*time.Second, 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.ErrorIs(t, server.Shutdown(ctx), errTaskFinalization)
	persisted, err := store.Get(t.Context(), task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "running", persisted.Status)
	require.Empty(t, persisted.Reply)
	_, err = db.ExecContext(t.Context(), `DROP TRIGGER fail_task_finalization ON acp_tasks`)
	require.NoError(t, err)
	require.NoError(t, store.interruptUnownedTask(t.Context(), task.TaskID))
	persisted, err = store.Get(t.Context(), task.TaskID)
	require.NoError(t, err)
	require.Equal(t, "interrupted", persisted.Status)
	require.Equal(t, int32(1), calls.Load())
}
