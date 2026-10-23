package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrCanvasUnavailable = errors.New("canvas store unavailable")

// CanvasStore retains custom catalog documents. The caller owns the pool.
type CanvasStore struct{ db *sql.DB }

func NewCanvasStore(db *sql.DB) *CanvasStore { return &CanvasStore{db: db} }
func (s *CanvasStore) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return ErrCanvasUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("canvas migration begin: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:canvas',0))`); err != nil {
		return fmt.Errorf("canvas migration lock: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS canvas_states (
 session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
 revision BIGINT NOT NULL CHECK (revision >= 0),
 state JSONB NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
 )`); err != nil {
		return fmt.Errorf("canvas migration: %w", err)
	}
	return tx.Commit()
}
func (s *CanvasStore) Get(ctx context.Context, sid string) (CanvasState, error) {
	empty := CanvasState{SessionID: sid, Surfaces: []CanvasSurface{}}
	if !canvasIdentifier(sid) {
		return empty, fmt.Errorf("%w: valid sessionId required", ErrCanvasInvalid)
	}
	if s == nil || s.db == nil {
		return empty, ErrCanvasUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var raw []byte
	var revision sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT c.state,c.revision FROM sessions s LEFT JOIN canvas_states c ON c.session_id=s.id WHERE s.id=$1`, sid).Scan(&raw, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrCanvasSessionNotFound
	}
	if err != nil {
		return empty, fmt.Errorf("canvas state: %w", err)
	}
	if !revision.Valid {
		return empty, nil
	}
	var state CanvasState
	if err = decodeCanvasJSON(raw, &state); err != nil {
		return empty, fmt.Errorf("canvas stored state: %w", err)
	}
	if state.SessionID != sid || state.Revision != revision.Int64 || state.Revision < 0 || state.Revision > CanvasMaxRevision {
		return empty, fmt.Errorf("canvas stored identity mismatch")
	}
	return state, nil
}
func (s *CanvasStore) Apply(ctx context.Context, sid, line string) (CanvasState, error) {
	mutation, err := parseCanvasMutation(sid, line)
	if err != nil {
		return CanvasState{}, fmt.Errorf("%w: %v", ErrCanvasInvalid, err)
	}
	return s.mutate(ctx, sid, &mutation)
}
func (s *CanvasStore) Reset(ctx context.Context, sid string) (CanvasState, error) {
	return s.mutate(ctx, sid, nil)
}
func (s *CanvasStore) mutate(ctx context.Context, sid string, mutation *canvasMutation) (CanvasState, error) {
	if !canvasIdentifier(sid) {
		return CanvasState{}, fmt.Errorf("%w: valid sessionId required", ErrCanvasInvalid)
	}
	if s == nil || s.db == nil {
		return CanvasState{}, ErrCanvasUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CanvasState{}, fmt.Errorf("canvas update begin: %w", err)
	}
	defer tx.Rollback()
	// Lock the existing session even before its first state row exists. This
	// serializes first creation and every later mutation across store instances.
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id=$1 FOR UPDATE`, sid).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return CanvasState{}, ErrCanvasSessionNotFound
	} else if err != nil {
		return CanvasState{}, fmt.Errorf("canvas session lock: %w", err)
	}
	state := CanvasState{SessionID: sid, Surfaces: []CanvasSurface{}}
	var raw []byte
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT state,revision FROM canvas_states WHERE session_id=$1`, sid).Scan(&raw, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CanvasState{}, fmt.Errorf("canvas update read: %w", err)
	}
	if err == nil {
		if err = decodeCanvasJSON(raw, &state); err != nil {
			return CanvasState{}, fmt.Errorf("canvas stored state: %w", err)
		}
		if state.SessionID != sid || state.Revision != revision || state.Revision < 0 || state.Revision > CanvasMaxRevision {
			return CanvasState{}, fmt.Errorf("canvas stored identity mismatch")
		}
	}
	if state.Revision >= CanvasMaxRevision {
		return CanvasState{}, fmt.Errorf("%w: canvas revision exhausted", ErrCanvasInvalid)
	}
	if mutation == nil {
		state.Surfaces = []CanvasSurface{}
		state.Revision++
	} else {
		state, err = applyCanvasMutation(state, *mutation)
		if err != nil {
			return CanvasState{}, fmt.Errorf("%w: %v", ErrCanvasInvalid, err)
		}
	}
	raw, err = json.Marshal(state)
	if err != nil {
		return CanvasState{}, fmt.Errorf("canvas encode: %w", err)
	}
	// Validate the final wrapper, whose nesting is deeper than the event's.
	// Every accepted document must remain readable by Get after persistence.
	if len(raw) > CanvasMaxStateBytes {
		return CanvasState{}, fmt.Errorf("%w: state exceeds %d bytes", ErrCanvasInvalid, CanvasMaxStateBytes)
	}
	var decoded CanvasState
	if err = decodeCanvasJSON(raw, &decoded); err != nil {
		return CanvasState{}, fmt.Errorf("%w: final state: %v", ErrCanvasInvalid, err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO canvas_states(session_id,revision,state) VALUES($1,$2,$3) ON CONFLICT(session_id) DO UPDATE SET revision=EXCLUDED.revision,state=EXCLUDED.state,updated_at=NOW()`, sid, state.Revision, string(raw)); err != nil {
		return CanvasState{}, fmt.Errorf("canvas persist: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return CanvasState{}, fmt.Errorf("canvas commit: %w", err)
	}
	return state, nil
}
