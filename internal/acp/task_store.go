package acp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type TaskStore struct{ db *sql.DB }

func NewTaskStore(db *sql.DB) *TaskStore { return &TaskStore{db: db} }
func taskDBContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}
func (s *TaskStore) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrTaskUnavailable
	}
	ctx, cancel := taskDBContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("acp tasks migrate begin: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':overkill:acp:tasks:migrate',0))`); err != nil {
		return fmt.Errorf("acp tasks migrate lock: %w", err)
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS acp_tasks(task_id TEXT PRIMARY KEY,message_id TEXT NOT NULL UNIQUE,session_id TEXT NOT NULL,session_ref TEXT REFERENCES sessions(id) ON DELETE SET NULL,key_hash TEXT NOT NULL UNIQUE,request_hash TEXT NOT NULL,content TEXT NOT NULL,from_label TEXT NOT NULL DEFAULT '',status TEXT NOT NULL CHECK(status IN ('queued','running','completed','failed','cancelled','interrupted')),reply TEXT NOT NULL DEFAULT '',error_msg TEXT NOT NULL DEFAULT '',progress TEXT NOT NULL DEFAULT '',created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`ALTER TABLE acp_tasks DROP CONSTRAINT IF EXISTS acp_tasks_session_id_fkey`,
		`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='acp_tasks'::regclass AND attname='session_ref' AND NOT attisdropped) THEN ALTER TABLE acp_tasks ADD COLUMN session_ref TEXT; UPDATE acp_tasks t SET session_ref=t.session_id WHERE EXISTS(SELECT 1 FROM sessions s WHERE s.id=t.session_id); END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='acp_tasks'::regclass AND conname='acp_tasks_session_ref_fkey') THEN ALTER TABLE acp_tasks ADD CONSTRAINT acp_tasks_session_ref_fkey FOREIGN KEY(session_ref) REFERENCES sessions(id) ON DELETE SET NULL; END IF; END $$`,
		`CREATE INDEX IF NOT EXISTS idx_acp_tasks_active ON acp_tasks(status,created_at)`,
	} {
		if _, err = tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("acp tasks migrate: %w", err)
		}
	}
	return tx.Commit()
}
func taskHash(value []byte) string { h := sha256.Sum256(value); return hex.EncodeToString(h[:]) }
func validateTaskSubmit(req TaskSubmitRequest) error {
	if strings.TrimSpace(req.IdempotencyKey) == "" || len(req.IdempotencyKey) > 128 || !utf8.ValidString(req.IdempotencyKey) {
		return fmt.Errorf("%w: idempotencyKey required, maximum128 bytes", ErrTaskInvalid)
	}
	if strings.TrimSpace(req.SessionID) == "" || len(req.SessionID) > 256 || !utf8.ValidString(req.SessionID) {
		return fmt.Errorf("%w: sessionID required", ErrTaskInvalid)
	}
	if strings.TrimSpace(req.Content) == "" || len(req.Content) > TaskMaxContentBytes || !utf8.ValidString(req.Content) {
		return fmt.Errorf("%w: content required, maximum%d bytes", ErrTaskInvalid, TaskMaxContentBytes)
	}
	if len(req.From) > 128 || !utf8.ValidString(req.From) {
		return fmt.Errorf("%w: from exceeds128 bytes", ErrTaskInvalid)
	}
	for _, v := range []string{req.IdempotencyKey, req.SessionID, req.From, req.Content} {
		if strings.ContainsRune(v, 0) {
			return fmt.Errorf("%w: NUL is not allowed", ErrTaskInvalid)
		}
	}
	return nil
}

const taskColumns = `task_id,message_id,session_id,status,reply,error_msg,progress,created_at,updated_at`

type taskScanner interface{ Scan(...any) error }

func scanTask(row taskScanner) (Task, error) {
	var task Task
	err := row.Scan(&task.TaskID, &task.MessageID, &task.SessionID, &task.Status, &task.Reply, &task.Error, &task.Progress, &task.CreatedAt, &task.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return task, ErrTaskNotFound
	}
	return task, err
}
func (s *TaskStore) Submit(ctx context.Context, req TaskSubmitRequest) (Task, error) {
	if err := validateTaskSubmit(req); err != nil {
		return Task{}, err
	}
	if s == nil || s.db == nil {
		return Task{}, ErrTaskUnavailable
	}
	ctx, cancel := taskDBContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	// Serializes active-capacity accounting and receipt allocation across servers.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':overkill:acp:tasks:submit',0))`); err != nil {
		return Task{}, err
	}
	key := taskHash([]byte(req.IdempotencyKey))
	body, _ := json.Marshal(struct{ From, Content, Session string }{req.From, req.Content, req.SessionID})
	hash := taskHash(body)
	var oldHash, id string
	err = tx.QueryRowContext(ctx, `SELECT request_hash,task_id FROM acp_tasks WHERE key_hash=$1`, key).Scan(&oldHash, &id)
	if err == nil {
		if oldHash != hash {
			return Task{}, ErrTaskConflict
		}
		task, e := scanTask(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM acp_tasks WHERE task_id=$1`, id))
		return task, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Task{}, err
	}
	var admittedSession string
	err = tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id=$1 FOR KEY SHARE`, req.SessionID).Scan(&admittedSession)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("%w: session does not exist", ErrTaskInvalid)
	}
	if err != nil {
		return Task{}, err
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM acp_tasks WHERE status IN ('queued','running')`).Scan(&active); err != nil {
		return Task{}, err
	}
	if active >= TaskMaxActive {
		return Task{}, ErrTaskCapacity
	}
	task, err := scanTask(tx.QueryRowContext(ctx, `INSERT INTO acp_tasks(task_id,message_id,session_id,session_ref,key_hash,request_hash,content,from_label,status) VALUES($1,$2,$3,$3,$4,$5,$6,$7,'queued') RETURNING `+taskColumns, uuid.NewString(), uuid.NewString(), req.SessionID, key, hash, req.Content, req.From))
	if err != nil {
		return Task{}, err
	}
	if err = tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}
func (s *TaskStore) Get(ctx context.Context, id string) (Task, error) {
	if s == nil || s.db == nil {
		return Task{}, ErrTaskUnavailable
	}
	ctx, cancel := taskDBContext(ctx)
	defer cancel()
	return scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM acp_tasks WHERE task_id=$1`, id))
}
func (s *TaskStore) Cancel(ctx context.Context, id string) (Task, error) {
	if s == nil || s.db == nil {
		return Task{}, ErrTaskUnavailable
	}
	ctx, cancel := taskDBContext(ctx)
	defer cancel()
	_, err := s.db.ExecContext(ctx, `UPDATE acp_tasks SET status='cancelled',error_msg='cancelled by caller',updated_at=NOW() WHERE task_id=$1 AND status IN ('queued','running')`, id)
	if err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}
func (s *TaskStore) Await(ctx context.Context, id string, wait time.Duration) (Task, error) {
	if wait < 0 || wait > TaskMaxAwait {
		return Task{}, ErrTaskInvalid
	}
	parent := ctx
	if wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	task, err := s.Get(ctx, id)
	if err != nil || task.Terminal() || wait == 0 {
		return task, err
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			if parent.Err() != nil {
				return Task{}, parent.Err()
			}
			return task, nil
		case <-tick.C:
			next, e := s.Get(ctx, id)
			if e != nil {
				if ctx.Err() != nil && parent.Err() == nil {
					return task, nil
				}
				return Task{}, e
			}
			task = next
			if task.Terminal() {
				return task, nil
			}
		}
	}
}

// Task/session locks live on a dedicated connection for the whole run. A peer
// may recover a running task only after acquiring that same task lock.
func taskTryLock(ctx context.Context, conn *sql.Conn, key string) (bool, error) {
	var ok bool
	err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended(current_schema()||':overkill:acp:'||$1,0))`, key).Scan(&ok)
	return ok, err
}
func taskUnlock(ctx context.Context, conn *sql.Conn, key string) error {
	var ok bool
	err := conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtextextended(current_schema()||':overkill:acp:'||$1,0))`, key).Scan(&ok)
	if err == nil && !ok {
		return fmt.Errorf("acp task lock ownership lost")
	}
	return err
}
func (s *TaskStore) candidates(ctx context.Context) ([]string, error) {
	ctx, cancel := taskDBContext(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT task_id FROM acp_tasks WHERE status IN ('queued','running') ORDER BY CASE WHEN status='running' THEN 0 ELSE 1 END,created_at,task_id LIMIT 256`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
