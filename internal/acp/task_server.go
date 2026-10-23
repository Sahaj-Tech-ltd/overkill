package acp

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// StartTasks starts this server's bounded durable task workers. Mounted Handler
// users call it explicitly; Handler itself never starts background work.
func (s *Server) StartTasks(ctx context.Context) error {
	if s.tasks == nil {
		return nil
	}
	if s.tasks.db == nil {
		return ErrTaskUnavailable
	}
	check, cancel := taskDBContext(ctx)
	defer cancel()
	if _, err := s.tasks.db.ExecContext(check, `SELECT 1 FROM acp_tasks LIMIT 0`); err != nil {
		return fmt.Errorf("acp task startup: %w", err)
	}
	maximum := s.tasks.db.Stats().MaxOpenConnections
	n := TaskConcurrency
	if maximum > 0 {
		if maximum < 3 {
			return fmt.Errorf("acp tasks require pool capacity of at least3")
		}
		if maximum-2 < n {
			n = maximum - 2
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return fmt.Errorf("acp server is closing")
	}
	if s.tasksStarted {
		if s.tasksStopped {
			return ErrTaskUnavailable
		}
		return nil
	}
	s.tasksStarted = true
	lifetime, cancelTasks := context.WithCancel(s.ctx)
	s.tasksCancel = cancelTasks
	stopParent := context.AfterFunc(ctx, cancelTasks)
	for range n {
		s.workers.Add(1)
		go func() { defer s.workers.Done(); s.taskWorker(lifetime) }()
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		<-lifetime.Done()
		stopParent()
		s.mu.Lock()
		s.tasksStopped = true
		if !s.closing {
			s.tasksFailure = "durable task worker lifetime ended; server restart required"
		}
		s.mu.Unlock()
	}()
	return nil
}
func (s *Server) taskWorker(ctx context.Context) {
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.taskSweep(ctx)
		s.mu.Lock()
		if errors.Is(err, errTaskFinalization) && ctx.Err() != nil {
			s.tasksShutdownErr = errors.Join(s.tasksShutdownErr, err)
		}
		if ctx.Err() == nil {
			if err != nil {
				s.tasksFailure = "durable worker stage failed; task retained for inspection"
			} else {
				s.tasksFailure = ""
			}
		}
		s.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			log.Printf("acp durable task worker: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

type taskLease struct {
	conn      *sql.Conn
	keys      []string
	discarded bool
	closed    bool
}

var errTaskFinalization = errors.New("durable task finalization unconfirmed")

func (l *taskLease) discard() {
	if !l.discarded {
		_ = l.conn.Raw(func(any) error { return driver.ErrBadConn })
		l.discarded = true
		l.keys = nil
	}
}

func (l *taskLease) lock(ctx context.Context, key string) (bool, error) {
	ok, err := taskTryLock(ctx, l.conn, key)
	if ok && err == nil {
		l.keys = append(l.keys, key)
	}
	return ok, err
}
func (l *taskLease) close() {
	if l == nil || l.conn == nil || l.closed {
		return
	}
	l.closed = true
	if l.discarded {
		_ = l.conn.Close()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := len(l.keys) - 1; i >= 0; i-- {
		if err := taskUnlock(ctx, l.conn, l.keys[i]); err != nil {
			log.Printf("acp release task ownership: %v", err)
			l.discard()
			break
		}
	}
	_ = l.conn.Close()
}
func (s *TaskStore) sessionLease(ctx context.Context, sid string) (*taskLease, error) {
	ctx, cancel := taskDBContext(ctx)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	lease := &taskLease{conn: conn}
	ok, err := lease.lock(ctx, "session:"+sid)
	if err != nil || !ok {
		lease.close()
		if err == nil {
			err = ErrTaskConflict
		}
		return nil, err
	}
	return lease, nil
}
func (l *taskLease) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, taskID string) func() {
	done := make(chan struct{})
	watchCtx, stop := context.WithCancel(ctx)
	go func() {
		defer close(done)
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-tick.C:
				queryCtx, queryCancel := context.WithTimeout(watchCtx, 2*time.Second)
				var status string
				var err error
				if taskID == "" {
					err = l.conn.QueryRowContext(queryCtx, `SELECT 'running'`).Scan(&status)
				} else {
					err = l.conn.QueryRowContext(queryCtx, `SELECT CASE WHEN session_ref IS NULL THEN 'session-deleted' ELSE status END FROM acp_tasks WHERE task_id=$1`, taskID).Scan(&status)
				}
				queryCancel()
				if err != nil {
					if watchCtx.Err() == nil {
						cancel(fmt.Errorf("task ownership connection unavailable"))
					}
					return
				}
				if status != "running" {
					cancel(fmt.Errorf("task cancellation requested"))
					return
				}
			}
		}
	}()
	return func() { stop(); <-done }
}
func (s *Server) taskSweep(ctx context.Context) error {
	ids, err := s.tasks.candidates(ctx)
	if err != nil {
		return fmt.Errorf("read task queue: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	dbctx, cancel := taskDBContext(ctx)
	conn, err := s.tasks.db.Conn(dbctx)
	cancel()
	if err != nil {
		return err
	}
	lease := &taskLease{conn: conn}
	defer lease.close()
	capacity := false
	for slot := range TaskConcurrency {
		dbctx, cancel = taskDBContext(ctx)
		ok, e := lease.lock(dbctx, "slot:"+strconv.Itoa(slot))
		cancel()
		if e != nil {
			return e
		}
		if ok {
			capacity = true
			break
		}
	}
	if !capacity {
		return nil
	}
	for _, id := range ids {
		dbctx, cancel = taskDBContext(ctx)
		ok, e := lease.lock(dbctx, "task:"+id)
		cancel()
		if e != nil {
			return e
		}
		if !ok {
			continue
		}
		processed, e := s.processTask(ctx, lease, id)
		if lease.discarded {
			return e
		}
		// Keep the task ownership until the state transition/run is fully complete.
		dbctx, cancel = taskDBContext(context.WithoutCancel(ctx))
		unlockErr := taskUnlock(dbctx, conn, "task:"+id)
		cancel()
		if unlockErr != nil {
			return unlockErr
		}
		lease.keys = lease.keys[:len(lease.keys)-1]
		if e != nil {
			return e
		}
		if processed {
			return nil
		}
	}
	return nil
}
func (s *Server) processTask(ctx context.Context, lease *taskLease, id string) (bool, error) {
	dbctx, cancel := taskDBContext(ctx)
	var status, sid, content string
	var sessionRef sql.NullString
	err := lease.conn.QueryRowContext(dbctx, `SELECT status,session_id,content,session_ref FROM acp_tasks WHERE task_id=$1`, id).Scan(&status, &sid, &content, &sessionRef)
	cancel()
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status == "running" {
		dbctx, cancel = taskDBContext(ctx)
		defer cancel()
		_, err = lease.conn.ExecContext(dbctx, `UPDATE acp_tasks SET status='interrupted',error_msg='previous execution ended without confirmed completion; explicit new task required',updated_at=NOW() WHERE task_id=$1 AND status='running'`, id)
		return true, err
	}
	if status != "queued" {
		return false, nil
	}
	if !sessionRef.Valid {
		dbctx, cancel = taskDBContext(ctx)
		defer cancel()
		_, err = lease.conn.ExecContext(dbctx, `UPDATE acp_tasks SET status='failed',error_msg='executing session was deleted before execution',updated_at=NOW() WHERE task_id=$1 AND status='queued'`, id)
		return true, err
	}
	dbctx, cancel = taskDBContext(ctx)
	ok, err := lease.lock(dbctx, "session:"+sid)
	cancel()
	if err != nil || !ok {
		return false, err
	}
	defer func() {
		if lease.discarded {
			return
		}
		releaseCtx, releaseCancel := taskDBContext(context.Background())
		defer releaseCancel()
		if e := taskUnlock(releaseCtx, lease.conn, "session:"+sid); e != nil {
			log.Printf("acp task session release: %v", e)
			lease.discard()
			return
		}
		lease.keys = lease.keys[:len(lease.keys)-1]
	}()
	dbctx, cancel = taskDBContext(ctx)
	result, err := lease.conn.ExecContext(dbctx, `UPDATE acp_tasks SET status='running',progress='starting',updated_at=NOW() WHERE task_id=$1 AND status='queued' AND session_ref IS NOT NULL`, id)
	cancel()
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return false, err
	}
	runCtx, timeout := context.WithTimeout(ctx, TaskRunTimeout)
	defer timeout()
	runCtx, cancelRun := context.WithCancelCause(runCtx)
	defer cancelRun(nil)
	stopHeartbeat := lease.heartbeat(runCtx, cancelRun, id)
	reply, progress, runErr := s.executeTask(runCtx, sid, content, lease, id)
	stopHeartbeat()
	terminal, reason := "completed", ""
	if ctx.Err() != nil {
		terminal, reason = "interrupted", "server stopped before confirmed completion"
	} else if runCtx.Err() != nil {
		terminal, reason = "interrupted", "execution cancelled before confirmed completion"
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			terminal, reason = "failed", "execution deadline exceeded"
		}
	} else if runErr != nil {
		terminal, reason = "failed", runErr.Error()
	}
	dbctx, cancel = taskDBContext(context.Background())
	defer cancel()
	_, err = lease.conn.ExecContext(dbctx, `UPDATE acp_tasks SET status=$2,reply=$3,error_msg=$4,progress=$5,updated_at=NOW() WHERE task_id=$1 AND status='running'`, id, terminal, reply, reason, progress)
	if err != nil {
		// lib/pq can invalidate the ownership connection when a checkpoint is
		// cancelled. Discard it before recovering on a fresh connection. Lost
		// ownership never authorizes claiming successful completion or publishing
		// uncommitted local reply text.
		lease.discard()
		if repairErr := s.tasks.interruptUnownedTask(context.Background(), id); repairErr != nil {
			return true, fmt.Errorf("%w: %v", errTaskFinalization, repairErr)
		}
	}
	return true, nil
}
func (s *Server) executeTask(ctx context.Context, sid, content string, lease *taskLease, id string) (string, string, error) {
	sender := s.agent
	if s.resolveSender != nil {
		var err error
		sender, err = s.resolveSender(ctx, sid)
		if err != nil {
			return "", "", fmt.Errorf("session resolver failed")
		}
	}
	if sender == nil || sender.SessionID() != sid {
		return "", "", fmt.Errorf("resolved agent does not match requested session")
	}
	events, err := sender.StreamACP(ctx, content)
	if err != nil {
		return "", "", fmt.Errorf("agent stream could not start")
	}
	if events == nil {
		return "", "", fmt.Errorf("agent returned no stream")
	}
	var reply strings.Builder
	progress := "running"
	lastCheckpoint := time.Time{}
	eventCount := 0
	for {
		select {
		case <-ctx.Done():
			return reply.String(), progress, ctx.Err()
		case event, ok := <-events:
			if !ok {
				return reply.String(), progress, fmt.Errorf("agent stream ended without completion")
			}
			eventCount++
			if eventCount > 16384 {
				return reply.String(), progress, fmt.Errorf("task event limit exceeded")
			}
			switch event.Type {
			case AgentEventToken:
				if !utf8.ValidString(event.Content) || strings.ContainsRune(event.Content, 0) {
					return reply.String(), progress, fmt.Errorf("agent returned invalid text")
				}
				if reply.Len()+len(event.Content) > TaskMaxReplyBytes {
					return reply.String(), progress, fmt.Errorf("reply exceeds task output limit")
				}
				reply.WriteString(event.Content)
			case AgentEventToolStart:
				progress = "tool running"
			case AgentEventToolOutput:
				progress = "tool finished"
			case AgentEventDone:
				return reply.String(), "completed", nil
			case AgentEventError:
				return reply.String(), progress, fmt.Errorf("agent reported an execution error")
			}
			// Checkpoint bounded reply/progress without persisting tool args, tool output,
			// provider error strings, model metadata, or authentication headers.
			if !lastCheckpoint.IsZero() && time.Since(lastCheckpoint) < 250*time.Millisecond {
				continue
			}
			lastCheckpoint = time.Now()
			dbctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, err = lease.conn.ExecContext(dbctx, `UPDATE acp_tasks SET reply=$2,progress=$3,updated_at=NOW() WHERE task_id=$1 AND status='running'`, id, reply.String(), progress)
			cancel()
			if err != nil {
				return reply.String(), progress, fmt.Errorf("task progress checkpoint failed")
			}
		}
	}
}
func decodeTaskRequest(r *http.Request, w http.ResponseWriter) (TaskSubmitRequest, error) {
	var req TaskSubmitRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxACPBody)
	data, err := io.ReadAll(r.Body)
	if err == nil && !utf8.Valid(data) {
		return req, ErrTaskInvalid
	}
	if err != nil {
		return req, err
	}
	// Decode a flat string object manually to reject duplicate/case aliases,
	// nulls, unknown members, and trailing values before receipt allocation.
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return req, ErrTaskInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return req, err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return req, ErrTaskInvalid
		}
		seen[key] = true
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return req, err
		}
		if len(raw) == 0 || raw[0] != '"' {
			return req, ErrTaskInvalid
		}
		var value string
		if err = json.Unmarshal(raw, &value); err != nil {
			return req, err
		}
		switch key {
		case "from":
			req.From = value
		case "content":
			req.Content = value
		case "sessionID":
			req.SessionID = value
		case "idempotencyKey":
			req.IdempotencyKey = value
		default:
			return req, ErrTaskInvalid
		}
	}
	if _, err = d.Token(); err != nil {
		return req, err
	}
	if _, err = d.Token(); err != io.EOF {
		return req, ErrTaskInvalid
	}
	return req, validateTaskSubmit(req)
}
func taskHTTPError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "task operation failed"
	switch {
	case errors.Is(err, ErrTaskInvalid):
		status, message = http.StatusBadRequest, err.Error()
	case errors.Is(err, ErrTaskConflict):
		status, message = http.StatusConflict, err.Error()
	case errors.Is(err, ErrTaskNotFound):
		status, message = http.StatusNotFound, err.Error()
	case errors.Is(err, ErrTaskCapacity):
		status, message = http.StatusTooManyRequests, err.Error()
	case errors.Is(err, ErrTaskUnavailable):
		status, message = http.StatusServiceUnavailable, err.Error()
	default:
		log.Printf("acp task HTTP operation: %v", err)
	}
	writeErrorJSON(w, status, message)
}
func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	req, err := decodeTaskRequest(r, w)
	if err != nil {
		taskHTTPError(w, fmt.Errorf("%w: invalid task body", ErrTaskInvalid))
		return
	}
	s.mu.Lock()
	closing := s.closing
	failure := s.tasksFailure
	started := s.tasksStarted && !s.tasksStopped
	s.mu.Unlock()
	if closing || !started || failure != "" {
		taskHTTPError(w, ErrTaskUnavailable)
		return
	}
	task, err := s.tasks.Submit(r.Context(), req)
	if err != nil {
		taskHTTPError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}
func (s *Server) handleTaskSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	parts := strings.Split(rest, "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		taskHTTPError(w, ErrTaskNotFound)
		return
	}
	id := parts[0]
	var task Task
	var err error
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if r.URL.RawQuery != "" {
			taskHTTPError(w, ErrTaskInvalid)
			return
		}
		task, err = s.tasks.Get(r.Context(), id)
	} else {
		switch parts[1] {
		case "cancel":
			if r.Method != http.MethodPost {
				writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			task, err = s.tasks.Cancel(r.Context(), id)
		case "await":
			if r.Method != http.MethodGet {
				writeErrorJSON(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			query, e := url.ParseQuery(r.URL.RawQuery)
			if e != nil || len(query) > 1 || len(query["timeoutMs"]) > 1 {
				taskHTTPError(w, ErrTaskInvalid)
				return
			}
			for key := range query {
				if key != "timeoutMs" {
					taskHTTPError(w, ErrTaskInvalid)
					return
				}
			}
			wait := TaskMaxAwait
			if raw, ok := query["timeoutMs"]; ok {
				ms, e := strconv.ParseInt(raw[0], 10, 64)
				if e != nil || ms < 0 || ms > 25000 {
					taskHTTPError(w, ErrTaskInvalid)
					return
				}
				wait = time.Duration(ms) * time.Millisecond
			}
			task, err = s.tasks.Await(r.Context(), id, wait)
		default:
			taskHTTPError(w, ErrTaskNotFound)
			return
		}
	}
	if err != nil {
		taskHTTPError(w, err)
		return
	}
	s.mu.Lock()
	task.WorkerError = s.tasksFailure
	if !s.tasksStarted {
		task.WorkerError = "durable task workers have not started"
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, task)
}

// interruptUnownedTask conservatively confirms a failed finalization after its
// old connection is discarded. Recovery must acquire the original task lock;
// an existing terminal result is preserved, never rewritten or replayed.
func (s *TaskStore) interruptUnownedTask(ctx context.Context, id string) error {
	ctx, cancel := taskDBContext(ctx)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	lease := &taskLease{conn: conn}
	defer lease.close()
	ok, err := lease.lock(ctx, "task:"+id)
	if err != nil {
		return err
	}
	if !ok {
		current, e := scanTask(conn.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM acp_tasks WHERE task_id=$1`, id))
		if e == nil && current.Terminal() {
			return nil
		}
		return fmt.Errorf("task ownership held by another worker; finalization remains unconfirmed")
	}
	result, err := conn.ExecContext(ctx, `UPDATE acp_tasks SET status='interrupted',error_msg='ownership connection lost before confirmed finalization; execution was not replayed',updated_at=NOW() WHERE task_id=$1 AND status='running'`, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed > 0 {
		return nil
	}
	current, err := scanTask(conn.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM acp_tasks WHERE task_id=$1`, id))
	if err != nil {
		return err
	}
	if !current.Terminal() {
		return fmt.Errorf("task is not terminal after finalization repair")
	}
	return nil
}
