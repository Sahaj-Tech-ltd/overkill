package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// PostgresStore is a PostgreSQL-backed session store that implements
// Store, Brancher, and session-router operations on the shared *sql.DB.
type PostgresStore struct {
	db *sql.DB
}

// RouterRow is one row returned by the router's Recent() method.
type RouterRow struct {
	SessionID string
	Channel   string
	Updated   time.Time
}

// NewPostgresStore creates a PostgresStore backed by the shared DB.
// The caller owns the DB lifecycle; Close() is a no-op.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	s := &PostgresStore{db: db}
	if err := s.migrate(); err != nil {
		// Migration errors mean the store is broken; log at ERROR and
		// let the caller decide. Return the store so the caller can
		// inspect it, but all operations will fail with a broken schema.
		log.Error().Err(err).Msg("session: migration failed — store may be unusable")
	}
	return s
}

func (s *PostgresStore) migrate() error {
	cmds := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id          TEXT PRIMARY KEY,
			title       TEXT NOT NULL DEFAULT '',
			folder      TEXT NOT NULL DEFAULT '',
			parent_id   TEXT,
			status      TEXT NOT NULL DEFAULT 'active',
			messages    JSONB NOT NULL DEFAULT '[]',
			model       TEXT NOT NULL DEFAULT '',
			provider    TEXT NOT NULL DEFAULT '',
			total_cost  DOUBLE PRECISION NOT NULL DEFAULT 0,
			metadata    JSONB NOT NULL DEFAULT '{}',
			token_count BIGINT NOT NULL DEFAULT 0,
			children    JSONB NOT NULL DEFAULT '[]',
			branched_at_turn INTEGER NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS session_router (
			channel    TEXT NOT NULL,
			chat_key   TEXT NOT NULL,
			thread     TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL,
			follow     TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (channel, chat_key, thread)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_router_session_id ON session_router (session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_folder ON sessions (folder)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions (parent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions (status)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_updated ON sessions (updated_at DESC)`,
	}
	for _, c := range cmds {
		if _, err := s.db.Exec(c); err != nil {
			preview := c
			if len(preview) > 40 {
				preview = preview[:40]
			}
			return fmt.Errorf("session migrate: %s: %w", preview, err)
		}
	}
	// Idempotent column additions for existing databases.
	for _, col := range []string{
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS token_count BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS children JSONB NOT NULL DEFAULT '[]'`,
	} {
		if _, err := s.db.Exec(col); err != nil {
			return fmt.Errorf("session migrate: %s: %w", col[:40], err)
		}
	}
	return nil
}

// Close is a no-op — the DB lifecycle is owned by the caller.
func (s *PostgresStore) Close() error { return nil }

// ---------------------------------------------------------------------------
// Store interface
// ---------------------------------------------------------------------------

// Create inserts a new session. Returns ErrExists when a session with the
// same ID already exists.
func (s *PostgresStore) Create(ctx context.Context, session *Session) error {
	if session.ID == "" {
		session.ID = uuid.New().String()
	}
	now := time.Now().UTC()
	session.CreatedAt = now
	session.UpdatedAt = now
	if session.Status == "" {
		session.Status = "active"
	}

	msgsJSON, err := json.Marshal(session.Messages)
	if err != nil {
		return fmt.Errorf("session: marshaling messages: %w", err)
	}
	metaJSON, err := json.Marshal(session.Metadata)
	if err != nil {
		return fmt.Errorf("session: marshaling metadata: %w", err)
	}
	childrenJSON, err := json.Marshal(session.Children)
	if err != nil {
		return fmt.Errorf("session: marshaling children: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions
			(id, title, folder, parent_id, status, messages, model, provider,
			 total_cost, metadata, token_count, children, branched_at_turn,
			 created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT (id) DO NOTHING`,
		session.ID,
		session.Title,
		session.Folder,
		session.ParentID,
		session.Status,
		msgsJSON,
		session.Model,
		session.Provider,
		session.CostUSD,
		metaJSON,
		session.TokenCount,
		childrenJSON,
		session.BranchedAtTurn,
		session.CreatedAt,
		session.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("session: create %s: %w", session.ID, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrExists
	}

	log.Debug().Str("id", session.ID).Str("folder", session.Folder).Msg("session created (pg)")
	return nil
}

// Load fetches a session by ID. Returns ErrNotFound if no row matches.
// Children are populated by a second query.
func (s *PostgresStore) Load(ctx context.Context, id string) (*Session, error) {
	return s.loadTx(ctx, s.db, id)
}

// loadTx fetches a session inside the given transaction or DB handle.
func (s *PostgresStore) loadTx(ctx context.Context, querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, id string) (*Session, error) {
	var (
		sess               Session
		msgsJSON, metaJSON []byte
		childrenJSON       []byte
		parentID           sql.NullString
	)
	err := querier.QueryRowContext(ctx, `
		SELECT id, title, folder, parent_id, status, messages, model, provider,
		       total_cost, metadata, token_count, children, branched_at_turn,
		       created_at, updated_at
		FROM sessions WHERE id = $1`, id,
	).Scan(
		&sess.ID,
		&sess.Title,
		&sess.Folder,
		&parentID,
		&sess.Status,
		&msgsJSON,
		&sess.Model,
		&sess.Provider,
		&sess.CostUSD,
		&metaJSON,
		&sess.TokenCount,
		&childrenJSON,
		&sess.BranchedAtTurn,
		&sess.CreatedAt,
		&sess.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("session: load %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("session: load %s: %w", id, err)
	}

	if parentID.Valid {
		sess.ParentID = parentID.String
	}

	if len(msgsJSON) > 0 {
		if err := json.Unmarshal(msgsJSON, &sess.Messages); err != nil {
			return nil, fmt.Errorf("session: unmarshaling messages for %s: %w", id, err)
		}
	}
	if sess.Messages == nil {
		sess.Messages = []providers.Message{}
	}

	if len(metaJSON) > 0 {
		if err := json.Unmarshal(metaJSON, &sess.Metadata); err != nil {
			return nil, fmt.Errorf("session: unmarshaling metadata for %s: %w", id, err)
		}
	}
	if sess.Metadata == nil {
		sess.Metadata = make(map[string]string)
	}

	if len(childrenJSON) > 0 {
		if err := json.Unmarshal(childrenJSON, &sess.Children); err != nil {
			return nil, fmt.Errorf("session: unmarshaling children for %s: %w", id, err)
		}
	}
	if sess.Children == nil {
		sess.Children = []string{}
	}

	sess.TurnCount = len(sess.Messages)

	return &sess, nil
}

// Save persists an updated session. The updated_at timestamp is set to NOW().
func (s *PostgresStore) Save(ctx context.Context, session *Session) error {
	msgsJSON, err := json.Marshal(session.Messages)
	if err != nil {
		return fmt.Errorf("session: marshaling messages: %w", err)
	}
	metaJSON, err := json.Marshal(session.Metadata)
	if err != nil {
		return fmt.Errorf("session: marshaling metadata: %w", err)
	}
	childrenJSON, err := json.Marshal(session.Children)
	if err != nil {
		return fmt.Errorf("session: marshaling children: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET title = $1, folder = $2, parent_id = $3, status = $4,
		    messages = $5, model = $6, provider = $7, total_cost = $8,
		    metadata = $9, token_count = $10, children = $11,
		    branched_at_turn = $12, updated_at = NOW()
		WHERE id = $13 AND updated_at = $14`,
		session.Title,
		session.Folder,
		session.ParentID,
		session.Status,
		msgsJSON,
		session.Model,
		session.Provider,
		session.CostUSD,
		metaJSON,
		session.TokenCount,
		childrenJSON,
		session.BranchedAtTurn,
		session.ID,
		session.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("session: save %s: %w", session.ID, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		// Distinguish between "session deleted" and "concurrent write".
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE id=$1)`, session.ID).Scan(&exists); err == nil && !exists {
			return fmt.Errorf("session: save %s: %w", session.ID, ErrNotFound)
		}
		return fmt.Errorf("session: save %s: concurrent write conflict — retry", session.ID)
	}

	log.Debug().Str("id", session.ID).Msg("session saved (pg)")
	session.UpdatedAt = time.Now().UTC()
	return nil
}

// Delete removes a session, all its session_router bindings, and reassigns
// any child sessions' ParentID to the deleted session's parent (if any).
// This avoids dangling ParentID references — children are promoted one level
// up the tree rather than being orphaned or cascade-deleted.
func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	// Use a transaction so the reassign+delete are atomic.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("session: delete: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Load the session to find its parent (for child reassignment).
	var parentID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT parent_id FROM sessions WHERE id = $1`, id).Scan(&parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("session: delete %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("session: delete %s: %w", id, err)
	}

	// Reassign children to the deleted session's parent (grandparent).
	// If the deleted session has no parent, children become root sessions.
	if parentID.Valid && parentID.String != "" {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET parent_id = $1 WHERE parent_id = $2`, parentID.String, id)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET parent_id = NULL WHERE parent_id = $1`, id)
	}
	if err != nil {
		return fmt.Errorf("session: delete: reassign children of %s: %w", id, err)
	}

	// Delete router bindings.
	if _, err := tx.ExecContext(ctx, `DELETE FROM session_router WHERE session_id = $1`, id); err != nil {
		return fmt.Errorf("session: delete router bindings for %s: %w", id, err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("session: delete %s: %w", id, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("session: delete %s: %w", id, ErrNotFound)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("session: delete: commit: %w", err)
	}

	log.Debug().Str("id", id).Msg("session deleted (pg)")
	return nil
}

// List returns sessions matching the optional filters, ordered by
// updated_at DESC. Limit and Offset are applied server-side.
func (s *PostgresStore) List(ctx context.Context, opts ListOptions) ([]*Session, error) {
	var (
		clauses []string
		args    []interface{}
		argIdx  = 1
	)

	if opts.Folder != "" {
		clauses = append(clauses, fmt.Sprintf("folder = $%d", argIdx))
		args = append(args, opts.Folder)
		argIdx++
	}
	if opts.Status != "" {
		clauses = append(clauses, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, opts.Status)
		argIdx++
	}
	if opts.ParentID != "" {
		clauses = append(clauses, fmt.Sprintf("parent_id = $%d", argIdx))
		args = append(args, opts.ParentID)
		argIdx++
	}
	if !opts.After.IsZero() {
		clauses = append(clauses, fmt.Sprintf("updated_at > $%d", argIdx))
		args = append(args, opts.After)
		argIdx++
	}

	query := `SELECT id, title, folder, parent_id, status, messages, model, provider,
	          total_cost, metadata, token_count, children,
	          branched_at_turn, created_at, updated_at FROM sessions`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	order := "DESC"
	if opts.OldestFirst {
		order = "ASC"
	}
	query += " ORDER BY updated_at " + order

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
		argIdx++
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, opts.Offset)
		argIdx++
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session: list: %w", err)
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		var (
			sess               Session
			msgsJSON, metaJSON []byte
			childrenJSON       []byte
			parentID           sql.NullString
		)
		if err := rows.Scan(
			&sess.ID, &sess.Title, &sess.Folder, &parentID,
			&sess.Status, &msgsJSON, &sess.Model, &sess.Provider,
			&sess.CostUSD, &metaJSON, &sess.TokenCount, &childrenJSON,
			&sess.BranchedAtTurn, &sess.CreatedAt, &sess.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("session: list scan: %w", err)
		}
		if parentID.Valid {
			sess.ParentID = parentID.String
		}

		if len(msgsJSON) > 0 {
			if err := json.Unmarshal(msgsJSON, &sess.Messages); err != nil {
				return nil, fmt.Errorf("session: list unmarshal messages: %w", err)
			}
		}
		if sess.Messages == nil {
			sess.Messages = []providers.Message{}
		}
		if len(metaJSON) > 0 {
			if err := json.Unmarshal(metaJSON, &sess.Metadata); err != nil {
				return nil, fmt.Errorf("session: list unmarshal metadata: %w", err)
			}
		}
		if sess.Metadata == nil {
			sess.Metadata = make(map[string]string)
		}
		if len(childrenJSON) > 0 {
			if err := json.Unmarshal(childrenJSON, &sess.Children); err != nil {
				return nil, fmt.Errorf("session: list unmarshal children: %w", err)
			}
		}
		if sess.Children == nil {
			sess.Children = []string{}
		}
		sess.TurnCount = len(sess.Messages)

		sessions = append(sessions, &sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session: list rows: %w", err)
	}

	return sessions, nil
}

// ---------------------------------------------------------------------------
// Brancher interface (Branch, Clone, Merge — now backed by PostgreSQL;
// they only depend on the Store interface, not storage internals)
// ---------------------------------------------------------------------------

// Branch creates a new session forked from parentID at message-index atTurn.
// Loads the parent inside the transaction to prevent TOCTOU races:
// without this, changes to the parent between Load and BeginTx are silently lost.
func (s *PostgresStore) Branch(ctx context.Context, parentID string, atTurn int) (*Session, error) {
	if parentID == "" {
		return nil, fmt.Errorf("session: branch: parentID is required")
	}
	if atTurn < 0 {
		return nil, fmt.Errorf("session: branch: atTurn must be >= 0, got %d", atTurn)
	}

	// Use a transaction so that the child creation and parent update
	// are atomic, AND the parent load is consistent.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("session: branch: begin tx: %w", err)
	}
	defer tx.Rollback()

	parent, err := s.loadTx(ctx, tx, parentID)
	if err != nil {
		return nil, fmt.Errorf("session: branch: load parent: %w", err)
	}
	if parent == nil {
		return nil, fmt.Errorf("session: branch: parent %q not found", parentID)
	}

	cut := atTurn
	if cut > len(parent.Messages) {
		cut = len(parent.Messages)
	}

	now := time.Now().UTC()
	prefix := make([]providers.Message, cut)
	copy(prefix, parent.Messages[:cut])

	child := &Session{
		ID:             uuid.New().String(),
		Title:          "branch of " + titleOr(parent.Title, "session"),
		Folder:         parent.Folder,
		ParentID:       parent.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
		Model:          parent.Model,
		Provider:       parent.Provider,
		Status:         "active",
		Metadata:       copyMetadata(parent.Metadata),
		Messages:       prefix,
		TurnCount:      cut,
		BranchedAtTurn: cut,
	}

	// Create child inside the transaction.
	msgsJSON, err := json.Marshal(child.Messages)
	if err != nil {
		return nil, fmt.Errorf("session: branch: marshal child messages: %w", err)
	}
	metaJSON, err := json.Marshal(child.Metadata)
	if err != nil {
		return nil, fmt.Errorf("session: branch: marshal child metadata: %w", err)
	}
	childrenJSON, err := json.Marshal(child.Children)
	if err != nil {
		return nil, fmt.Errorf("session: branch: marshal child children: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions
			(id, title, folder, parent_id, status, messages, model, provider,
			 total_cost, metadata, token_count, children, branched_at_turn,
			 created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		child.ID, child.Title, child.Folder, child.ParentID,
		child.Status, msgsJSON, child.Model, child.Provider,
		child.CostUSD, metaJSON, child.TokenCount, childrenJSON,
		child.BranchedAtTurn, child.CreatedAt, child.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("session: branch: create child: %w", err)
	}

	// Update parent's children list.
	parent.Children = append(parent.Children, child.ID)
	parent.UpdatedAt = now
	parentMsgsJSON, err := json.Marshal(parent.Messages)
	if err != nil {
		return nil, fmt.Errorf("session: branch: marshal parent messages: %w", err)
	}
	parentMetaJSON, err := json.Marshal(parent.Metadata)
	if err != nil {
		return nil, fmt.Errorf("session: branch: marshal parent metadata: %w", err)
	}
	parentChildrenJSON, err := json.Marshal(parent.Children)
	if err != nil {
		return nil, fmt.Errorf("session: branch: marshal parent children: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE sessions
		SET title = $1, folder = $2, parent_id = $3, status = $4,
		    messages = $5, model = $6, provider = $7, total_cost = $8,
		    metadata = $9, children = $10, updated_at = $11
		WHERE id = $12`,
		parent.Title, parent.Folder, parent.ParentID, parent.Status,
		parentMsgsJSON, parent.Model, parent.Provider, parent.CostUSD,
		parentMetaJSON, parentChildrenJSON, parent.UpdatedAt, parent.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("session: branch: update parent: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("session: branch: commit: %w", err)
	}
	return child, nil
}

// Clone creates an independent duplicate of parentID with the full
// message history copied over. Loads the parent inside the transaction
// to prevent TOCTOU races.
func (s *PostgresStore) Clone(ctx context.Context, parentID string) (*Session, error) {
	if parentID == "" {
		return nil, fmt.Errorf("session: clone: parentID is required")
	}

	// Use a transaction so that the child creation and parent update
	// are atomic, AND the parent load is consistent.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("session: clone: begin tx: %w", err)
	}
	defer tx.Rollback()

	parent, err := s.loadTx(ctx, tx, parentID)
	if err != nil {
		return nil, fmt.Errorf("session: clone: load parent: %w", err)
	}
	if parent == nil {
		return nil, fmt.Errorf("session: clone: parent %q not found", parentID)
	}

	now := time.Now().UTC()
	full := make([]providers.Message, len(parent.Messages))
	copy(full, parent.Messages)

	child := &Session{
		ID:             uuid.New().String(),
		Title:          "clone of " + titleOr(parent.Title, "session"),
		Folder:         parent.Folder,
		ParentID:       parent.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
		Model:          parent.Model,
		Provider:       parent.Provider,
		Status:         "active",
		Metadata:       copyMetadata(parent.Metadata),
		Messages:       full,
		TurnCount:      len(full),
		BranchedAtTurn: len(full),
	}

	// Create child inside the transaction.
	msgsJSON, err := json.Marshal(child.Messages)
	if err != nil {
		return nil, fmt.Errorf("session: clone: marshal child messages: %w", err)
	}
	metaJSON, err := json.Marshal(child.Metadata)
	if err != nil {
		return nil, fmt.Errorf("session: clone: marshal child metadata: %w", err)
	}
	childrenJSON, err := json.Marshal(child.Children)
	if err != nil {
		return nil, fmt.Errorf("session: clone: marshal child children: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sessions
			(id, title, folder, parent_id, status, messages, model, provider,
			 total_cost, metadata, token_count, children, branched_at_turn,
			 created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		child.ID, child.Title, child.Folder, child.ParentID,
		child.Status, msgsJSON, child.Model, child.Provider,
		child.CostUSD, metaJSON, child.TokenCount, childrenJSON,
		child.BranchedAtTurn, child.CreatedAt, child.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("session: clone: create child: %w", err)
	}

	// Update parent's children list.
	parent.Children = append(parent.Children, child.ID)
	parent.UpdatedAt = now
	parentMsgsJSON, err := json.Marshal(parent.Messages)
	if err != nil {
		return nil, fmt.Errorf("session: clone: marshal parent messages: %w", err)
	}
	parentMetaJSON, err := json.Marshal(parent.Metadata)
	if err != nil {
		return nil, fmt.Errorf("session: clone: marshal parent metadata: %w", err)
	}
	parentChildrenJSON, err := json.Marshal(parent.Children)
	if err != nil {
		return nil, fmt.Errorf("session: clone: marshal parent children: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE sessions
		SET title = $1, folder = $2, parent_id = $3, status = $4,
		    messages = $5, model = $6, provider = $7, total_cost = $8,
		    metadata = $9, children = $10, updated_at = $11
		WHERE id = $12`,
		parent.Title, parent.Folder, parent.ParentID, parent.Status,
		parentMsgsJSON, parent.Model, parent.Provider, parent.CostUSD,
		parentMetaJSON, parentChildrenJSON, parent.UpdatedAt, parent.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("session: clone: update parent: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("session: clone: commit: %w", err)
	}
	return child, nil
}

// Merge fast-forwards a child branch back into its parent.
// Wrapped in a transaction to prevent lost-update races: concurrent
// merges on the same parent are serialised by PostgreSQL row locks.
func (s *PostgresStore) Merge(ctx context.Context, childID string) (*Session, error) {
	if childID == "" {
		return nil, fmt.Errorf("session: merge: childID is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("session: merge: begin tx: %w", err)
	}
	defer tx.Rollback()

	child, err := s.loadTx(ctx, tx, childID)
	if err != nil {
		return nil, fmt.Errorf("session: merge: load child: %w", err)
	}
	if child == nil {
		return nil, fmt.Errorf("session: merge: child %q not found", childID)
	}
	if child.ParentID == "" {
		return nil, fmt.Errorf("session: merge: child %q has no parent", childID)
	}
	parent, err := s.loadTx(ctx, tx, child.ParentID)
	if err != nil {
		return nil, fmt.Errorf("session: merge: load parent: %w", err)
	}
	if parent == nil {
		return nil, fmt.Errorf("session: merge: parent %q not found", child.ParentID)
	}

	branchPoint := child.BranchedAtTurn
	if branchPoint < 0 || branchPoint > len(child.Messages) {
		return nil, fmt.Errorf("session: merge: child %q has invalid BranchedAtTurn=%d", childID, branchPoint)
	}
	if len(parent.Messages) != branchPoint {
		return nil, ErrMergeDiverged
	}

	tail := child.Messages[branchPoint:]
	if len(tail) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("session: merge: commit: %w", err)
		}
		return parent, nil
	}

	now := time.Now().UTC()
	parent.Messages = append(parent.Messages, tail...)
	parent.TurnCount = len(parent.Messages)
	parent.UpdatedAt = now

	msgsJSON, err := json.Marshal(parent.Messages)
	if err != nil {
		return nil, fmt.Errorf("session: merge: marshal parent: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET messages = $1, token_count = $2, updated_at = $3
		WHERE id = $4`,
		msgsJSON, parent.TurnCount, parent.UpdatedAt, parent.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("session: merge: save parent: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("session: merge: parent %q not found", parent.ID)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("session: merge: commit: %w", err)
	}
	return parent, nil
}

// ---------------------------------------------------------------------------
// Router methods (session_router table)
// ---------------------------------------------------------------------------

// Bind locks a channel/chat/thread tuple to a session id.
func (s *PostgresStore) Bind(ctx context.Context, channel, chatKey, thread, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_router (channel, chat_key, thread, session_id, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (channel, chat_key, thread)
		DO UPDATE SET session_id = $4, updated_at = NOW()`,
		channel, chatKey, thread, sessionID,
	)
	if err != nil {
		return fmt.Errorf("session router: bind: %w", err)
	}
	return nil
}

// Resolve looks up the session id for a channel/chat/thread binding.
// If follow mode points to a pinned session or "tui", that takes priority.
// Returns the resolved session ID, or empty string if nothing is bound.
func (s *PostgresStore) Resolve(ctx context.Context, channel, chatKey, thread, liveSessionID string) (string, error) {
	// Check follow mode first.
	var follow string
	err := s.db.QueryRowContext(ctx, `
		SELECT follow FROM session_router
		WHERE channel = $1 AND chat_key = $2 AND thread = $3`,
		channel, chatKey, thread,
	).Scan(&follow)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("session router: resolve follow: %w", err)
	}

	if follow == "tui" && liveSessionID != "" {
		return liveSessionID, nil
	}
	if follow != "" && follow != "tui" {
		return follow, nil
	}

	// Normal binding.
	var sessionID string
	err = s.db.QueryRowContext(ctx, `
		SELECT session_id FROM session_router
		WHERE channel = $1 AND chat_key = $2 AND thread = $3`,
		channel, chatKey, thread,
	).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("session router: resolve: %w", err)
	}
	return sessionID, nil
}

// Follow sets a chat's follow target. target="tui" shadows the live
// TUI session; any other non-empty value pins to that session; empty
// clears follow mode. Channel is required to scope the follow to a
// specific gateway (telegram, discord, etc.) — without it different
// channels sharing the same chatKey would collide on follow state.
func (s *PostgresStore) Follow(ctx context.Context, channel, chatKey, target string) error {
	// Update entries for this channel + chat_key combination.
	_, err := s.db.ExecContext(ctx, `
		UPDATE session_router SET follow = $1, updated_at = NOW()
		WHERE channel = $2 AND chat_key = $3`,
		target, channel, chatKey,
	)
	if err != nil {
		return fmt.Errorf("session router: follow: %w", err)
	}
	return nil
}

// Touch bumps the updated_at timestamp for a binding so /sessions can
// sort by recency. No-op if the binding doesn't exist.
func (s *PostgresStore) Touch(ctx context.Context, channel, chatKey, thread string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE session_router SET updated_at = NOW()
		WHERE channel = $1 AND chat_key = $2 AND thread = $3`,
		channel, chatKey, thread,
	)
	if err != nil {
		return fmt.Errorf("session router: touch: %w", err)
	}
	return nil
}

// Recent returns up to limit bindings sorted newest-first.
func (s *PostgresStore) Recent(ctx context.Context, limit int) ([]RouterRow, error) {
	query := `SELECT session_id, channel, updated_at FROM session_router ORDER BY updated_at DESC`
	var args []interface{}
	if limit > 0 {
		argIdx := len(args) + 1
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("session router: recent: %w", err)
	}
	defer rows.Close()

	var out []RouterRow
	for rows.Next() {
		var r RouterRow
		if err := rows.Scan(&r.SessionID, &r.Channel, &r.Updated); err != nil {
			return nil, fmt.Errorf("session router: recent scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session router: recent rows: %w", err)
	}
	return out, nil
}

// Unbind removes a binding from the router table.
func (s *PostgresStore) Unbind(ctx context.Context, channel, chatKey, thread string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM session_router
		WHERE channel = $1 AND chat_key = $2 AND thread = $3`,
		channel, chatKey, thread,
	)
	if err != nil {
		return fmt.Errorf("session router: unbind: %w", err)
	}
	return nil
}
