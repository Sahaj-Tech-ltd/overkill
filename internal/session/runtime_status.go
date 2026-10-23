package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// RuntimeStatusStore persists actual observed evaluation/check state separately
// from transcript saves, so one writer cannot overwrite the other's metadata.
type RuntimeStatusStore interface {
	SaveRuntimeStatus(context.Context, string, json.RawMessage) error
	LoadRuntimeStatus(context.Context, string) (json.RawMessage, error)
}

const MaxRuntimeStatusBytes = 2 * 1024 * 1024

func (s *PostgresStore) SaveRuntimeStatus(ctx context.Context, id string, data json.RawMessage) error {
	if id == "" || len(data) > MaxRuntimeStatusBytes || !json.Valid(data) {
		return fmt.Errorf("session: invalid runtime status")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO session_runtime_status(session_id,payload) SELECT id,$2::jsonb FROM sessions WHERE id=$1 ON CONFLICT(session_id) DO UPDATE SET payload=EXCLUDED.payload,updated_at=NOW()`, id, []byte(data))
	if err != nil {
		return fmt.Errorf("session: write runtime status: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) LoadRuntimeStatus(ctx context.Context, id string) (json.RawMessage, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM session_runtime_status WHERE session_id=$1 AND octet_length(payload::text)<=$2`, id, MaxRuntimeStatusBytes).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: read runtime status: %w", err)
	}
	return json.RawMessage(data), nil
}
