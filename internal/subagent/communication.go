package subagent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const ParentWorker = "parent"

var (
	ErrWorkerUnavailable         = errors.New("worker: recipient is unavailable; completed work requires an explicit follow-up")
	ErrMessageConflict           = errors.New("worker: idempotency key was already used with different content")
	ErrMessageAdmissionUncertain = errors.New("worker: delivery commit could not be confirmed; reconnect and inspect worker history before continuing; do not replay effects")
	workerLabel                  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

// CommunicationStore is an application-bound chat and runtime incarnation.
// Neither scope, sender nor incarnation comes from a model's JSON. A new
// runtime can inspect old evidence, but cannot send into an old worker run.
type CommunicationStore struct {
	db                 *sql.DB
	scope, incarnation string
}

type WorkerRecord struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Goal          string    `json:"goal"`
	Model         string    `json:"model"`
	Status        string    `json:"status"`
	Live          bool      `json:"live"`
	FollowupOf    string    `json:"followup_of,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	Result        *Result   `json:"result,omitempty"`
	Tools         []string  `json:"-"`
	GoalTruncated bool      `json:"goal_truncated,omitempty"`
	SummaryOnly   bool      `json:"summary_only,omitempty"`
}

type WorkerMessage struct {
	ID        string    `json:"id"`
	Sequence  int64     `json:"sequence"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	InReplyTo string    `json:"in_reply_to,omitempty"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

func NewCommunicationStore(ctx context.Context, db *sql.DB, scope string) (*CommunicationStore, error) {
	if db == nil || scope == "" || len(scope) > 128 {
		return nil, fmt.Errorf("worker: database and bounded chat identity required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:worker-mailboxes',0))`); err != nil {
		return nil, err
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS worker_runs(scope TEXT NOT NULL,id TEXT NOT NULL,incarnation TEXT NOT NULL,name TEXT NOT NULL,goal TEXT NOT NULL,model TEXT NOT NULL,status TEXT NOT NULL,followup_of TEXT NOT NULL DEFAULT '',tools JSONB NOT NULL,result JSONB,started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(scope,id))`,
		`CREATE INDEX IF NOT EXISTS idx_worker_runs_live ON worker_runs(scope,incarnation,status,name)`,
		`CREATE TABLE IF NOT EXISTS worker_messages(id TEXT PRIMARY KEY,sequence BIGSERIAL UNIQUE,scope TEXT NOT NULL,incarnation TEXT NOT NULL,from_worker TEXT NOT NULL,to_worker TEXT NOT NULL,kind TEXT NOT NULL,content TEXT NOT NULL,in_reply_to TEXT NOT NULL DEFAULT '',state TEXT NOT NULL DEFAULT 'accepted',idempotency_key TEXT NOT NULL,request_hash BYTEA NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),UNIQUE(scope,incarnation,from_worker,idempotency_key))`,
		`CREATE TABLE IF NOT EXISTS worker_terminal_handoffs(id TEXT PRIMARY KEY,scope TEXT NOT NULL,incarnation TEXT NOT NULL,terminal_id TEXT NOT NULL,from_worker TEXT NOT NULL,from_revision BIGINT NOT NULL,state TEXT NOT NULL,request_hash BYTEA NOT NULL,receipt JSONB NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),UNIQUE(scope,incarnation,terminal_id,from_revision))`,
		`CREATE INDEX IF NOT EXISTS idx_worker_messages_inbox ON worker_messages(scope,incarnation,to_worker,sequence)`,
	} {
		if _, err = tx.ExecContext(ctx, ddl); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &CommunicationStore{db: db, scope: scope, incarnation: uuid.NewString()}, nil
}

func (s *CommunicationStore) transaction(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:worker-scope:' || $1,0))`, s.scope)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// RegisterBatch is all-or-nothing, before any worker/provider starts.
func (s *CommunicationStore) RegisterBatch(ctx context.Context, workers []WorkerRecord) error {
	tx, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, w := range workers {
		if w.ID == "" || w.ID == ParentWorker || w.Name == ParentWorker || !workerLabel.MatchString(w.Name) || !validWorkerText(w.Goal, 16000) {
			return fmt.Errorf("worker: valid identity, label and bounded goal required")
		}
		var duplicate bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM worker_runs WHERE scope=$1 AND incarnation=$2 AND name=$3 AND status='running')`, s.scope, s.incarnation, w.Name).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			return fmt.Errorf("worker: label %q is already running", w.Name)
		}
		tools, err := json.Marshal(w.Tools)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO worker_runs(scope,id,incarnation,name,goal,model,status,followup_of,tools) VALUES($1,$2,$3,$4,$5,$6,'running',$7,$8)`, s.scope, w.ID, s.incarnation, w.Name, w.Goal, w.Model, w.FollowupOf, tools)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validWorkerText(text string, maximum int) bool {
	return text != "" && len(text) <= maximum && utf8.ValidString(text)
}

func (s *CommunicationStore) Finish(ctx context.Context, id string, result *Result) error {
	if result == nil {
		return fmt.Errorf("worker: result required")
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(data) > 512*1024 {
		return fmt.Errorf("worker: result evidence exceeds 512 KiB")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE worker_runs SET status=$4,result=$5 WHERE scope=$1 AND incarnation=$2 AND id=$3 AND status='running'`, s.scope, s.incarnation, id, result.Status, data)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrWorkerUnavailable
	}
	return err
}

func (s *CommunicationStore) sender(ctx context.Context, tx *sql.Tx, id string, active bool) error {
	if id == ParentWorker {
		return nil
	}
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM worker_runs WHERE scope=$1 AND incarnation=$2 AND id=$3`, s.scope, s.incarnation, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrWorkerUnavailable
	}
	if err != nil {
		return err
	}
	if active && status != "running" {
		return ErrWorkerUnavailable
	}
	return nil
}

const workerMessageColumns = `id,sequence,from_worker,to_worker,kind,content,in_reply_to,state,created_at`

type messageScanner interface{ Scan(...any) error }

func scanWorkerMessage(row messageScanner) (WorkerMessage, error) {
	var m WorkerMessage
	err := row.Scan(&m.ID, &m.Sequence, &m.From, &m.To, &m.Kind, &m.Content, &m.InReplyTo, &m.State, &m.CreatedAt)
	return m, err
}

// Send stores updates/questions/answers, never new task authority or a stop.
// The authenticated parent may propose steering; sibling messages remain data.
func (s *CommunicationStore) Send(ctx context.Context, from, to, kind, content, key, reply string) (WorkerMessage, error) {
	if !validWorkerText(content, 16000) || !validWorkerText(key, 128) {
		return WorkerMessage{}, fmt.Errorf("worker: content and idempotency key must be bounded nonempty UTF-8")
	}
	if kind != "update" && kind != "question" && kind != "answer" && !(kind == "steer" && from == ParentWorker) {
		return WorkerMessage{}, fmt.Errorf("worker: unsupported message intent")
	}
	if kind == "answer" && reply == "" {
		return WorkerMessage{}, fmt.Errorf("worker: answer requires in_reply_to")
	}
	if from == to {
		return WorkerMessage{}, fmt.Errorf("worker: choose a different recipient")
	}
	request, _ := json.Marshal([]string{to, kind, content, reply})
	hash := sha256.Sum256(request)
	tx, err := s.transaction(ctx)
	if err != nil {
		return WorkerMessage{}, err
	}
	defer tx.Rollback()
	if err = s.sender(ctx, tx, from, false); err != nil {
		return WorkerMessage{}, err
	}
	var stored []byte
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id,request_hash FROM worker_messages WHERE scope=$1 AND incarnation=$2 AND from_worker=$3 AND idempotency_key=$4`, s.scope, s.incarnation, from, key).Scan(&existing, &stored)
	if err == nil {
		if string(stored) != string(hash[:]) {
			return WorkerMessage{}, ErrMessageConflict
		}
		m, err := scanWorkerMessage(tx.QueryRowContext(ctx, `SELECT `+workerMessageColumns+` FROM worker_messages WHERE id=$1`, existing))
		return m, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WorkerMessage{}, err
	}
	if err = s.sender(ctx, tx, from, true); err != nil {
		return WorkerMessage{}, err
	}
	if to != ParentWorker {
		var id string
		err = tx.QueryRowContext(ctx, `SELECT id FROM worker_runs WHERE scope=$1 AND incarnation=$2 AND status='running' AND (id=$3 OR name=$3)`, s.scope, s.incarnation, to).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return WorkerMessage{}, ErrWorkerUnavailable
		}
		if err != nil {
			return WorkerMessage{}, err
		}
		to = id
	}
	if from == to {
		return WorkerMessage{}, fmt.Errorf("worker: choose a different recipient")
	}
	if reply != "" {
		var correlated bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM worker_messages WHERE id=$1 AND scope=$2 AND incarnation=$3 AND from_worker=$4 AND to_worker=$5)`, reply, s.scope, s.incarnation, to, from).Scan(&correlated)
		if err != nil {
			return WorkerMessage{}, err
		}
		if !correlated {
			return WorkerMessage{}, fmt.Errorf("worker: reply does not match this correspondence")
		}
	}
	var pending, total int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE to_worker=$3 AND state='accepted'),count(*) FROM worker_messages WHERE scope=$1 AND incarnation=$2`, s.scope, s.incarnation, to).Scan(&pending, &total)
	if err != nil {
		return WorkerMessage{}, err
	}
	if pending >= 128 || total >= 4096 {
		return WorkerMessage{}, fmt.Errorf("worker: bounded mailbox capacity reached")
	}
	m, err := scanWorkerMessage(tx.QueryRowContext(ctx, `INSERT INTO worker_messages(id,scope,incarnation,from_worker,to_worker,kind,content,in_reply_to,idempotency_key,request_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+workerMessageColumns, uuid.NewString(), s.scope, s.incarnation, from, to, kind, content, reply, key, hash[:]))
	if err != nil {
		return WorkerMessage{}, err
	}
	return m, tx.Commit()
}

// Inbox exposes this endpoint's correspondence. The owning parent can audit
// the whole chat's worker messages. Siblings cannot read each other's unrelated
// correspondence or private model contexts.
func (s *CommunicationStore) Inbox(ctx context.Context, worker string, after int64) ([]WorkerMessage, error) {
	return s.inbox(ctx, worker, after, false)
}
func (s *CommunicationStore) RecentInbox(ctx context.Context, worker string) ([]WorkerMessage, error) {
	return s.inbox(ctx, worker, 0, true)
}
func (s *CommunicationStore) inbox(ctx context.Context, worker string, after int64, recent bool) ([]WorkerMessage, error) {
	if after < 0 {
		return nil, fmt.Errorf("worker: nonnegative cursor required")
	}
	tx, err := s.transaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.sender(ctx, tx, worker, false); err != nil {
		return nil, err
	}
	order := "sequence"
	if recent {
		order += " DESC"
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+workerMessageColumns+` FROM worker_messages WHERE scope=$1 AND (incarnation=$2 OR $3='parent') AND ($3='parent' OR to_worker=$3 OR from_worker=$3) AND sequence>$4 ORDER BY `+order+` LIMIT 100`, s.scope, s.incarnation, worker, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []WorkerMessage{}
	for rows.Next() {
		m, err := scanWorkerMessage(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	if recent {
		slices.Reverse(result)
	}
	return result, rows.Err()
}

// Admit runs at a model boundary, after all preceding tool results. The state
// means context admission, not understanding, processing or task completion.
func (s *CommunicationStore) Admit(ctx context.Context, worker string, consume func([]WorkerMessage) error) error {
	tx, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.sender(ctx, tx, worker, true); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+workerMessageColumns+` FROM worker_messages WHERE scope=$1 AND (incarnation=$2 OR $3='parent') AND to_worker=$3 AND state='accepted' ORDER BY sequence LIMIT 4 FOR UPDATE`, s.scope, s.incarnation, worker)
	if err != nil {
		return err
	}
	var batch []WorkerMessage
	for rows.Next() {
		m, err := scanWorkerMessage(rows)
		if err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(batch) == 0 {
		return nil
	}
	if err = consume(batch); err != nil {
		return err
	}
	for _, m := range batch {
		if _, err = tx.ExecContext(ctx, `UPDATE worker_messages SET state='admitted' WHERE id=$1`, m.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return s.resolveAdmissionCommit(worker, batch, err)
	}
	return nil
}

// A missing COMMIT response is not proof of rollback. Reacquiring the same
// scope lock waits for the old transaction to finish, then inspects the exact
// batch. Confirmed commits keep their context; confirmed rollbacks can retry.
// An unavailable or inconsistent observation stops this runtime for review.
func (s *CommunicationStore) resolveAdmissionCommit(worker string, batch []WorkerMessage, cause error) error {
	unknown := func() error { return fmt.Errorf("%w: %w", ErrMessageAdmissionUncertain, cause) }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, err := s.transaction(ctx)
	if err != nil {
		return unknown()
	}
	defer tx.Rollback()
	ids := make([]string, len(batch))
	for i, m := range batch {
		ids[i] = m.ID
	}
	var total, accepted, admitted int
	err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE state='accepted'),count(*) FILTER(WHERE state IN ('admitted','acknowledged')) FROM worker_messages WHERE scope=$1 AND to_worker=$2 AND id=ANY($3)`, s.scope, worker, pq.Array(ids)).Scan(&total, &accepted, &admitted)
	if err != nil || total != len(batch) || total == 0 {
		return unknown()
	}
	if admitted == total {
		return nil
	}
	if accepted == total {
		return cause
	}
	return unknown()
}

func (s *CommunicationStore) Acknowledge(ctx context.Context, worker, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE worker_messages SET state='acknowledged' WHERE scope=$1 AND (incarnation=$2 OR $3='parent') AND to_worker=$3 AND id=$4 AND state IN ('admitted','acknowledged')`, s.scope, s.incarnation, worker, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return fmt.Errorf("worker: only an admitted recipient message may be acknowledged")
	}
	return err
}

func (s *CommunicationStore) Workers(ctx context.Context, historical bool) ([]WorkerRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,goal,model,status,incarnation=$2,followup_of,started_at,result,tools FROM worker_runs WHERE scope=$1 AND ($3 OR incarnation=$2) ORDER BY started_at DESC,id LIMIT 100`, s.scope, s.incarnation, historical)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkerRecord{}
	for rows.Next() {
		w, err := scanWorkerRecord(rows)
		if err != nil {
			return nil, err
		}
		if len(w.Goal) > 1024 {
			w.Goal = workerTextPrefix(w.Goal, 1024)
			w.GoalTruncated = true
		}
		if w.Result != nil {
			w.Result = &Result{WorkerID: w.Result.WorkerID, Status: w.Result.Status, Summary: workerTextPrefix(w.Result.Summary, 4096), Error: workerTextPrefix(w.Result.Error, 1024)}
			w.SummaryOnly = true
		}
		w.Tools = nil
		out = append(out, w)
	}
	return out, rows.Err()
}

func workerTextPrefix(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return strings.ToValidUTF8(text[:n], "") + "…"
}
func scanWorkerRecord(row messageScanner) (WorkerRecord, error) {
	var w WorkerRecord
	var result, tools []byte
	if err := row.Scan(&w.ID, &w.Name, &w.Goal, &w.Model, &w.Status, &w.Live, &w.FollowupOf, &w.StartedAt, &result, &tools); err != nil {
		return w, err
	}
	if len(result) > 0 {
		if err := json.Unmarshal(result, &w.Result); err != nil {
			return w, err
		}
	}
	if err := json.Unmarshal(tools, &w.Tools); err != nil {
		return w, err
	}
	return w, nil
}

// ReadWorker is used by trusted parent follow-up admission, not a sibling list.
func (s *CommunicationStore) ReadWorker(ctx context.Context, id string) (WorkerRecord, error) {
	return scanWorkerRecord(s.db.QueryRowContext(ctx, `SELECT id,name,goal,model,status,incarnation=$2,followup_of,started_at,result,tools FROM worker_runs WHERE scope=$1 AND id=$3`, s.scope, s.incarnation, id))
}
func (s *CommunicationStore) SetModel(ctx context.Context, id, model string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE worker_runs SET model=$4 WHERE scope=$1 AND incarnation=$2 AND id=$3 AND status='running'`, s.scope, s.incarnation, id, model)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrWorkerUnavailable
	}
	return err
}
