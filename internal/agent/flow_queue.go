package agent

// Durable resume admission belongs to the application, not model tool input.
// Pending checkpoints recover automatically. A process lost during an executing
// continuation is retained for review because tool side effects cannot be
// rolled back by a PostgreSQL transaction.
import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/google/uuid"
)

type FlowQueue struct {
	stopped atomic.Bool
	paused  sync.Map
	db      *sql.DB
	delay   time.Duration
	store   *PostgresFlowStore
}
type SessionFlowStore struct {
	epoch                                 atomic.Int64
	queue                                 *FlowQueue
	sessionID, workspace, model, provider string
}
type FlowLease struct {
	queue      *FlowQueue
	conn       *sql.Conn
	State      *FlowState
	Generation int64
	owner      string
	key        string
}

func NewFlowQueue(db *sql.DB, delay time.Duration) (*FlowQueue, error) {
	store, err := NewPostgresFlowStore(db)
	if err != nil {
		return nil, err
	}
	if delay <= 0 {
		delay = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':flow_resume_jobs:migrate',0))`); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS flow_resume_jobs (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
 workspace TEXT NOT NULL, generation BIGINT NOT NULL DEFAULT 1,
 status TEXT NOT NULL, due_at TIMESTAMPTZ NOT NULL, owner TEXT NOT NULL DEFAULT '',
 detail TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
 ); CREATE INDEX IF NOT EXISTS flow_resume_jobs_due ON flow_resume_jobs(status,due_at);
 CREATE TABLE IF NOT EXISTS flow_resume_owners(session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,stop_epoch BIGINT NOT NULL DEFAULT 0)`)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &FlowQueue{db: db, delay: delay, store: store}, nil
}
func (q *FlowQueue) Bind(sessionID, workspace, model, provider string) *SessionFlowStore {
	return &SessionFlowStore{queue: q, sessionID: sessionID, workspace: workspace, model: model, provider: provider}
}

func (s *SessionFlowStore) Save(state *FlowState) error {
	if state == nil || state.ID == "" || state.SessionID != s.sessionID {
		return errors.New("flow checkpoint: invalid owning session")
	}
	copy := *state
	copy.CancelEpoch = s.epoch.Load()
	copy.Workspace = s.workspace
	if copy.Model == "" {
		copy.Model = s.model
	}
	copy.Provider = s.provider
	if err := validateSequentialCheckpoint(copy.Sequential); err != nil {
		return err
	}
	if _, err := validateLoadedFlowPlan(&copy); err != nil {
		return err
	}
	status := "paused"
	if strings.HasPrefix(copy.Reason, "exceeded max steps") || copy.Reason == "deadline_exceeded" {
		status = "queued"
	}
	if _, paused := s.queue.paused.Load(s.sessionID); s.queue.stopped.Load() || paused {
		status = "paused"
	}
	if !flowHistoryComplete(copy.History) {
		status = "manual_review"
	}
	if status == "queued" {
		for i := len(copy.History) - 1; i >= 0; i-- {
			message := copy.History[i]
			if message.Role == "system" {
				continue
			}
			if message.Role == "assistant" && len(message.ToolCalls) == 0 && (copy.LoadedPlan == nil || copy.LoadedPlan.Started) && (copy.Sequential == nil || copy.Sequential.Started) {
				status = "manual_review"
			}
			break
		}
	}

	if copy.Resumes >= MaxResumes && status == "queued" {
		status = "exhausted"
	}
	copy.ResumeStatus = status
	data, err := json.Marshal(&copy)
	if err != nil {
		return err
	}
	if len(data) > 16<<20 {
		return errors.New("flow checkpoint exceeds durable state size limit")
	}
	history, err := json.Marshal(copy.History)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := s.queue.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A single commit makes the completed transcript durable before the wake can
	// be observed. The transaction also fences concurrent queue generations.
	var folder, sessionStatus string
	if err = tx.QueryRowContext(ctx, `SELECT folder,status FROM sessions WHERE id=$1 FOR UPDATE`, s.sessionID).Scan(&folder, &sessionStatus); err != nil {
		return err
	}
	if sessionStatus != "active" {
		return errors.New("flow checkpoint: owning session is inactive")
	}
	canonical, err := filepath.EvalSymlinks(folder)
	if err != nil || filepath.Clean(canonical) != s.workspace {
		return errors.New("flow checkpoint: owning workspace changed")
	}
	var epoch int64
	if err = tx.QueryRowContext(ctx, `SELECT stop_epoch FROM flow_resume_owners WHERE session_id=$1 FOR UPDATE`, s.sessionID).Scan(&epoch); err != nil {
		return err
	}
	if epoch != copy.CancelEpoch {
		return errors.New("flow checkpoint: superseded by explicit stop")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO flow_states(id,session_id,state,updated_at) VALUES($1,$2,$3,NOW())
 ON CONFLICT(id) DO UPDATE SET state=EXCLUDED.state,updated_at=NOW() WHERE flow_states.session_id=EXCLUDED.session_id`, copy.ID, s.sessionID, data)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("flow checkpoint: flow belongs to another session")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE sessions SET messages=$2,model=$3,provider=$4,updated_at=NOW() WHERE id=$1`, s.sessionID, history, copy.Model, copy.Provider); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO flow_resume_jobs(id,session_id,workspace,status,due_at) VALUES($1,$2,$3,$4,NOW()+$5::interval)
 ON CONFLICT(id) DO UPDATE SET generation=flow_resume_jobs.generation+1,status=EXCLUDED.status,due_at=EXCLUDED.due_at,owner='',detail='',updated_at=NOW()
 WHERE flow_resume_jobs.session_id=EXCLUDED.session_id AND flow_resume_jobs.workspace=EXCLUDED.workspace`, copy.ID, s.sessionID, s.workspace, status, fmt.Sprintf("%f seconds", s.queue.delay.Seconds()))
	if err != nil {
		return err
	}
	n, _ = result.RowsAffected()
	if n != 1 {
		return errors.New("flow checkpoint: immutable owner mismatch")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	state.ResumeStatus = status
	state.CancelEpoch = copy.CancelEpoch
	state.Workspace = copy.Workspace
	state.Model = copy.Model
	state.Provider = copy.Provider
	return nil
}
func (s *SessionFlowStore) Load(id string) (*FlowState, error) {
	state, err := s.queue.store.Load(id)
	if err != nil || state == nil {
		return state, err
	}
	if state.SessionID != s.sessionID {
		return nil, errors.New("flow: session mismatch")
	}
	return state, nil
}
func (s *SessionFlowStore) Delete(id string) error {
	state, err := s.Load(id)
	if err != nil || state == nil {
		return err
	}
	_, err = s.queue.db.Exec(`DELETE FROM flow_states WHERE id=$1 AND session_id=$2`, id, s.sessionID)
	return err
}
func (s *SessionFlowStore) List() ([]*FlowState, error) {
	states, err := s.queue.store.List()
	if err != nil {
		return nil, err
	}
	var result []*FlowState
	for _, state := range states {
		if state.SessionID == s.sessionID {
			result = append(result, state)
		}
	}
	return result, nil
}

func (q *FlowQueue) Due(ctx context.Context) ([]string, error) {
	if q.stopped.Load() {
		return nil, nil
	}
	rows, err := q.db.QueryContext(ctx, `SELECT id FROM flow_resume_jobs WHERE (status='queued' AND due_at<=NOW()) OR status='running' ORDER BY CASE status WHEN 'queued' THEN 0 ELSE 1 END,due_at,id LIMIT 32`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (q *FlowQueue) Session(ctx context.Context, id string) (string, error) {
	var sessionID string
	err := q.db.QueryRowContext(ctx, `SELECT session_id FROM flow_resume_jobs WHERE id=$1`, id).Scan(&sessionID)
	return sessionID, err
}

// Claim retains a dedicated database advisory lock until Close. Other app or
// daemon processes cannot execute this flow concurrently; process death releases
// the lock, allowing the next owner to classify a running job as ambiguous.
func (q *FlowQueue) Claim(ctx context.Context, id string) (*FlowLease, error) {
	if q.stopped.Load() {
		return nil, nil
	}
	owner, err := q.Session(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, paused := q.paused.Load(owner); paused {
		return nil, nil
	}
	conn, err := q.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	lease := &FlowLease{queue: q, conn: conn, key: id, owner: uuid.NewString()}
	var acquired bool
	err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended(current_schema()||':flow-resume:'||$1,0))`, id).Scan(&acquired)
	if err != nil || !acquired {
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			lease.Close()
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var status string
	var due bool
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT j.generation,j.status,j.due_at<=NOW(),s.state FROM flow_resume_jobs j JOIN flow_states s ON s.id=j.id AND s.session_id=j.session_id WHERE j.id=$1 FOR UPDATE OF j,s`, id).Scan(&lease.Generation, &status, &due, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `UPDATE flow_resume_jobs SET status='manual_review',owner='',detail='durable checkpoint is missing',updated_at=NOW() WHERE id=$1 AND status IN('queued','running')`, id); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if status == "running" {
		_, err = tx.ExecContext(ctx, `UPDATE flow_resume_jobs SET status='manual_review',owner='',detail='previous owner stopped during execution; inspect tool effects before retry',updated_at=NOW() WHERE id=$1`, id)
		if err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	if status != "queued" || !due {
		return nil, nil
	}
	var state FlowState
	decodeErr := json.Unmarshal(raw, &state)
	_, planErr := validateLoadedFlowPlan(&state)
	if len(raw) > 16<<20 || decodeErr != nil || planErr != nil || state.ID != id || state.SessionID == "" || state.Workspace == "" || !flowHistoryComplete(state.History) {
		if _, err := tx.ExecContext(ctx, `UPDATE flow_resume_jobs SET status='manual_review',owner='',detail='checkpoint is corrupt or contains unresolved tool execution',updated_at=NOW() WHERE id=$1`, id); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, ErrFlowCorrupt
	}
	if state.Resumes >= MaxResumes {
		_, err = tx.ExecContext(ctx, `UPDATE flow_resume_jobs SET status='exhausted',updated_at=NOW() WHERE id=$1`, id)
		if err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	state.Resumes++
	state.ResumedAt = append(state.ResumedAt, time.Now().UTC())
	raw, err = json.Marshal(&state)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE flow_states SET state=$2,updated_at=NOW() WHERE id=$1`, id, raw); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE flow_resume_jobs SET status='running',owner=$2,updated_at=NOW() WHERE id=$1`, id, lease.owner); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	lease.State = &state
	keep = true
	return lease, nil
}
func (l *FlowLease) Finish(ctx context.Context, status, detail string) error {
	switch status {
	case "completed", "cancelled", "manual_review", "failed":
	default:
		return errors.New("flow: invalid final status")
	}
	tx, err := l.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stateID string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM flow_states WHERE id=$1 FOR UPDATE`, l.key).Scan(&stateID); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE flow_resume_jobs SET status=$4,detail=$5,owner='',updated_at=NOW() WHERE id=$1 AND generation=$2 AND owner=$3 AND status='running'`, l.key, l.Generation, l.owner, status, detail)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 1 && status == "completed" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM flow_states WHERE id=$1 AND session_id=$2`, l.key, l.State.SessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (l *FlowLease) Close() {
	if l == nil || l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := l.conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended(current_schema()||':flow-resume:'||$1,0))`, l.key); err != nil {
		_ = l.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	l.conn.Close()
	l.conn = nil
}

func flowHistoryComplete(history []providers.Message) bool {
	pending := map[string]bool{}
	for _, message := range history {
		if message.Role == "assistant" {
			for _, call := range message.ToolCalls {
				if call.ID == "" || pending[call.ID] {
					return false
				}
				pending[call.ID] = true
			}
		}
		if message.Role == "tool" {
			if message.ToolCallID == "" || !pending[message.ToolCallID] {
				return false
			}
			delete(pending, message.ToolCallID)
			lower := strings.ToLower(message.Content)
			if strings.Contains(lower, "context canceled") || strings.Contains(lower, "deadline exceeded") {
				return false
			}
		}
	}
	return len(pending) == 0
}

// BeginTurn refreshes the cancellation fence only for a new human turn.
func (s *SessionFlowStore) BeginTurn(ctx context.Context) error {
	if _, err := s.queue.db.ExecContext(ctx, `INSERT INTO flow_resume_owners(session_id) VALUES($1) ON CONFLICT DO NOTHING`, s.sessionID); err != nil {
		return err
	}
	var epoch int64
	if err := s.queue.db.QueryRowContext(ctx, `SELECT stop_epoch FROM flow_resume_owners WHERE session_id=$1`, s.sessionID).Scan(&epoch); err != nil {
		return err
	}
	s.epoch.Store(epoch)
	return nil
}
func (s *SessionFlowStore) ContinueTurn(epoch int64) { s.epoch.Store(epoch) }

// PauseSession is a durable stop latch. Even a late checkpoint from an older
// in-flight operation cannot queue work after the user explicitly stopped it.
func (q *FlowQueue) PauseSession(ctx context.Context, sessionID string) error {
	q.paused.Store(sessionID, true)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id=$1 FOR UPDATE`, sessionID).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO flow_resume_owners(session_id,stop_epoch) VALUES($1,1) ON CONFLICT(session_id) DO UPDATE SET stop_epoch=flow_resume_owners.stop_epoch+1`, sessionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE flow_resume_jobs SET status='paused',generation=generation+1,owner='',detail='explicitly stopped',updated_at=NOW() WHERE session_id=$1 AND status IN('queued','running')`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

type FlowJobStatus struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Status     string    `json:"status"`
	DueAt      time.Time `json:"due_at"`
	Detail     string    `json:"detail"`
	Generation int64     `json:"generation"`
}

func (q *FlowQueue) ListSession(ctx context.Context, sessionID string) ([]FlowJobStatus, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT id,session_id,status,due_at,detail,generation FROM flow_resume_jobs WHERE session_id=$1 ORDER BY updated_at DESC LIMIT 100`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FlowJobStatus{}
	for rows.Next() {
		var job FlowJobStatus
		if err := rows.Scan(&job.ID, &job.SessionID, &job.Status, &job.DueAt, &job.Detail, &job.Generation); err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}
func (q *FlowQueue) PauseAll(ctx context.Context) error {
	q.stopped.Store(true)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := q.db.QueryContext(ctx, `SELECT session_id FROM flow_resume_owners UNION SELECT session_id FROM flow_resume_jobs WHERE status IN('queued','running')`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := q.PauseSession(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// TrySessionOperation shares ordinary human/API and background admission across
// application processes. The connection lock spans execution and persistence.
func (q *FlowQueue) TrySessionOperation(ctx context.Context, sessionID string) (func(), bool, error) {
	conn, err := q.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended(current_schema()||':session-runtime:'||$1,0))`, sessionID).Scan(&acquired)
	if err != nil || !acquired {
		if err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		conn.Close()
		return nil, false, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended(current_schema()||':session-runtime:'||$1,0))`, sessionID); err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			conn.Close()
		})
	}
	return release, true, nil
}

func (q *FlowQueue) Wake(ctx context.Context, id string) error {
	result, err := q.db.ExecContext(ctx, `UPDATE flow_resume_jobs SET due_at=NOW(),updated_at=NOW() WHERE id=$1 AND status='queued'`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("flow is not queued; inspect its status before retry")
	}
	return nil
}

func (s *SessionFlowStore) BeginHumanTurn(ctx context.Context) error {
	if err := s.queue.PauseSession(ctx, s.sessionID); err != nil {
		return err
	}
	if err := s.BeginTurn(ctx); err != nil {
		return err
	}
	s.queue.paused.Delete(s.sessionID)
	return nil
}

func (q *FlowQueue) DeferBusy(ctx context.Context, id string) {
	_, _ = q.db.ExecContext(ctx, `UPDATE flow_resume_jobs SET due_at=NOW()+INTERVAL '1 second' WHERE id=$1 AND status='queued'`, id)
}
