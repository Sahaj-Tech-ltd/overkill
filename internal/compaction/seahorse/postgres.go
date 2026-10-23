package seahorse

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// PostgresStore never deletes historical nodes or raw sources during compaction.
// It uses the caller's connection/search_path; no global database is selected.
type storeLimits struct {
	sources, nodes int64
	bytes          int64
}

var defaultStoreLimits = storeLimits{sources: 100000, nodes: 20000, bytes: 512 * 1024 * 1024}

type PostgresStore struct {
	db     *sql.DB
	limits storeLimits
}

func NewPostgresStore(ctx context.Context, db *sql.DB) (*PostgresStore, error) {
	if db == nil {
		return nil, fmt.Errorf("seahorse: database is required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize idempotent schema initialization across factory callers. PostgreSQL
	// CREATE TABLE IF NOT EXISTS alone can race on its catalog unique indexes.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(571683210044)`); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS seahorse_sessions(session_id TEXT PRIMARY KEY, revision BIGINT NOT NULL DEFAULT 0, state JSONB NOT NULL DEFAULT '{}');
 CREATE TABLE IF NOT EXISTS seahorse_sources(session_id TEXT NOT NULL, source_id TEXT NOT NULL, payload JSONB NOT NULL, PRIMARY KEY(session_id,source_id));
 CREATE TABLE IF NOT EXISTS seahorse_nodes(session_id TEXT NOT NULL,node_id TEXT NOT NULL,payload JSONB NOT NULL,PRIMARY KEY(session_id,node_id));
 CREATE INDEX IF NOT EXISTS seahorse_sources_fts ON seahorse_sources USING GIN (to_tsvector('simple',payload::text));`)
	if err != nil {
		return nil, fmt.Errorf("seahorse: initialize store: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &PostgresStore{db: db, limits: defaultStoreLimits}, nil
}
func (s *PostgresStore) Load(ctx context.Context, session string) (Snapshot, error) {
	var state Snapshot
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT revision,state FROM seahorse_sessions WHERE session_id=$1`, session).Scan(&state.Revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	revision := state.Revision
	if err = json.Unmarshal(raw, &state); err != nil {
		return Snapshot{}, err
	}
	state.Revision = revision
	return state, nil
}
func (s *PostgresStore) Node(ctx context.Context, session, id string) (Node, error) {
	var raw []byte
	var node Node
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM seahorse_nodes WHERE session_id=$1 AND node_id=$2`, session, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return node, ErrNotFound
	}
	if err != nil {
		return node, err
	}
	if err = json.Unmarshal(raw, &node); err != nil {
		return node, err
	}
	if node.Hash != id {
		return Node{}, fmt.Errorf("seahorse: node identity mismatch")
	}
	return node, validateNode(node)
}
func (s *PostgresStore) Source(ctx context.Context, session, id string) (Source, error) {
	var raw []byte
	var source Source
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM seahorse_sources WHERE session_id=$1 AND source_id=$2`, session, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return source, ErrNotFound
	}
	if err != nil {
		return source, err
	}
	if err = json.Unmarshal(raw, &source); err != nil {
		return source, err
	}
	if source.ID != id || sourceFor(source.Message).ID != id {
		return Source{}, fmt.Errorf("seahorse: corrupt source hash")
	}
	return source, nil
}
func (s *PostgresStore) Commit(ctx context.Context, session string, revision int64, sources []Source, nodes []Node, next Snapshot) error {
	if session == "" || len(session) > 256 || len(sources) > 4096 || len(nodes) > 256 {
		return fmt.Errorf("seahorse: invalid commit bounds")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO seahorse_sessions(session_id) VALUES($1) ON CONFLICT DO NOTHING`, session); err != nil {
		return err
	}
	var current int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM seahorse_sessions WHERE session_id=$1 FOR UPDATE`, session).Scan(&current); err != nil {
		return err
	}
	if current != revision {
		return ErrConflict
	}
	for _, source := range sources {
		if sourceFor(source.Message).ID != source.ID {
			return fmt.Errorf("seahorse: invalid source hash")
		}
		raw, e := json.Marshal(source)
		if e != nil {
			return e
		}
		if len(raw) > 4*1024*1024 {
			return fmt.Errorf("seahorse: source exceeds bound")
		}
		inserted, e := tx.ExecContext(ctx, `INSERT INTO seahorse_sources(session_id,source_id,payload) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, session, source.ID, string(raw))
		if e != nil {
			return e
		}
		count, e := inserted.RowsAffected()
		if e != nil {
			return e
		}
		if count == 0 {
			var existingRaw []byte
			if e = tx.QueryRowContext(ctx, `SELECT payload FROM seahorse_sources WHERE session_id=$1 AND source_id=$2`, session, source.ID).Scan(&existingRaw); e != nil {
				return e
			}
			var existing Source
			if e = json.Unmarshal(existingRaw, &existing); e != nil {
				return e
			}
			if existing.ID != source.ID || sourceFor(existing.Message).ID != source.ID {
				return fmt.Errorf("seahorse: existing source hash mismatch")
			}
		}
	}
	for _, node := range nodes {
		if err = validateNode(node); err != nil {
			return err
		}
		maxDepth := -1
		for _, id := range node.Summary.ParentIDs {
			var raw []byte
			if err = tx.QueryRowContext(ctx, `SELECT payload FROM seahorse_nodes WHERE session_id=$1 AND node_id=$2`, session, id).Scan(&raw); err != nil {
				return fmt.Errorf("seahorse: missing parent: %w", err)
			}
			var parent Node
			if err = json.Unmarshal(raw, &parent); err != nil {
				return err
			}
			if err = validateNode(parent); err != nil {
				return err
			}
			if parent.Hash != id {
				return fmt.Errorf("seahorse: parent identity mismatch")
			}
			maxDepth = max(maxDepth, parent.Summary.Depth)
		}
		if node.Summary.Kind == KindCondensed && node.Summary.Depth != maxDepth+1 {
			return fmt.Errorf("seahorse: invalid node depth")
		}
		for _, id := range node.SourceIDs {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM seahorse_sources WHERE session_id=$1 AND source_id=$2)`, session, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("seahorse: missing raw source")
			}
		}
		raw, e := json.Marshal(node)
		if e != nil {
			return e
		}
		if len(raw) > 1024*1024 {
			return fmt.Errorf("seahorse: node exceeds bound")
		}
		inserted, e := tx.ExecContext(ctx, `INSERT INTO seahorse_nodes(session_id,node_id,payload) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, session, node.Hash, string(raw))
		if e != nil {
			return e
		}
		count, e := inserted.RowsAffected()
		if e != nil {
			return e
		}
		if count == 0 {
			var existingRaw []byte
			if e = tx.QueryRowContext(ctx, `SELECT payload FROM seahorse_nodes WHERE session_id=$1 AND node_id=$2`, session, node.Hash).Scan(&existingRaw); e != nil {
				return e
			}
			var existing Node
			if e = json.Unmarshal(existingRaw, &existing); e != nil {
				return e
			}
			if e = validateNode(existing); e != nil {
				return e
			}
			if existing.Hash != node.Hash {
				return fmt.Errorf("seahorse: existing node identity mismatch")
			}
		}
	}
	// Bound the retained dataset per session. Refuse additional compaction
	// atomically instead of evicting raw evidence or silently dropping history.
	var sourceCount, nodeCount, totalBytes int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(octet_length(payload::text)),0) FROM seahorse_sources WHERE session_id=$1`, session).Scan(&sourceCount, &totalBytes); err != nil {
		return err
	}
	var nodeBytes int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(octet_length(payload::text)),0) FROM seahorse_nodes WHERE session_id=$1`, session).Scan(&nodeCount, &nodeBytes); err != nil {
		return err
	}
	if sourceCount > s.limits.sources || nodeCount > s.limits.nodes || totalBytes+nodeBytes > s.limits.bytes {
		return fmt.Errorf("seahorse: retained session dataset exceeds safety bound")
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM seahorse_nodes WHERE session_id=$1 AND node_id=$2)`, session, next.HeadID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("seahorse: missing head")
	}
	var headRaw []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM seahorse_nodes WHERE session_id=$1 AND node_id=$2`, session, next.HeadID).Scan(&headRaw); err != nil {
		return err
	}
	var head Node
	if err = json.Unmarshal(headRaw, &head); err != nil {
		return err
	}
	if err = validateNode(head); err != nil {
		return err
	}
	if head.Hash != next.HeadID || !validOutput(next.Output, head) || len(next.InputHash) != 64 {
		return fmt.Errorf("seahorse: invalid snapshot publication")
	}
	if err = validateToolGroups(next.Output); err != nil {
		return err
	}
	next.Revision = current + 1
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if len(raw) > 4*1024*1024 {
		return fmt.Errorf("seahorse: snapshot exceeds bound")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE seahorse_sessions SET revision=$2,state=$3 WHERE session_id=$1`, session, next.Revision, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
