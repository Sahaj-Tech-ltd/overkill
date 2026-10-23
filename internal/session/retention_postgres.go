package session

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *PostgresStore) enforceMaxSessions(ctx context.Context, max int) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return 0, fmt.Errorf("session retention begin: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':overkill:session-retention',0))`); err != nil {
		return 0, fmt.Errorf("session retention lock: %w", err)
	}
	// Materialize only initial root IDs in PostgreSQL, not full histories or an
	// unbounded Go slice. Temp storage can spill under PostgreSQL's own limits.
	if _, err = tx.ExecContext(ctx, `CREATE TEMP TABLE overkill_retention_candidates ON COMMIT DROP AS SELECT id FROM sessions WHERE parent_id IS NULL OR parent_id='' ORDER BY updated_at DESC,id DESC OFFSET $1`, max); err != nil {
		return 0, fmt.Errorf("session retention snapshot: %w", err)
	}
	deleted := 0
	for {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		rows, e := tx.QueryContext(ctx, `SELECT id FROM pg_temp.overkill_retention_candidates ORDER BY id LIMIT 128`)
		if e != nil {
			return 0, e
		}
		ids := make([]string, 0, 128)
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return 0, e
			}
			ids = append(ids, id)
		}
		if e = rows.Err(); e != nil {
			rows.Close()
			return 0, e
		}
		rows.Close()
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err = deleteSessionTx(ctx, tx, id); err != nil {
				return 0, err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM pg_temp.overkill_retention_candidates WHERE id=$1`, id); err != nil {
				return 0, err
			}
			deleted++
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("session retention commit: %w", err)
	}
	return deleted, nil
}
