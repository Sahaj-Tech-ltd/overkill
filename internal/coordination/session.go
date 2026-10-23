package coordination

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// BindSession is a trusted application operation. Both records must exist and
// identify the same canonical workspace. A live peer cannot be retargeted to
// another conversation by changing model-visible metadata.
func (s *Store) BindSession(ctx context.Context, peerID, sessionID string) error {
	if _, err := uuid.Parse(sessionID); err != nil {
		return fmt.Errorf("coordination: valid persisted session ID required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	peer, err := enabledPeer(ctx, tx, peerID)
	if err != nil {
		return err
	}
	var folder string
	err = tx.QueryRowContext(ctx, `SELECT folder FROM sessions WHERE id=$1 FOR SHARE`, sessionID).Scan(&folder)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return err
	}
	if canonical != peer.Workspace {
		return fmt.Errorf("coordination: peer and session workspaces differ")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO coordination_sessions(peer_id,session_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, peerID, sessionID)
	if err != nil {
		return err
	}
	var bound string
	err = tx.QueryRowContext(ctx, `SELECT session_id FROM coordination_sessions WHERE peer_id=$1`, peerID).Scan(&bound)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if bound != sessionID {
		return ErrConflict
	}
	return tx.Commit()
}

func (s *Store) PeerForSession(ctx context.Context, sessionID string) (Peer, error) {
	peer, err := scanPeer(s.db.QueryRowContext(ctx, `SELECT p.id,p.project_id,p.name,p.role,p.workspace,p.capabilities,p.endpoint,p.enabled
		FROM coordination_peers p JOIN coordination_sessions b ON b.peer_id=p.id
		WHERE b.session_id=$1 AND p.enabled`, sessionID))
	if errors.Is(err, ErrUnauthorized) {
		return Peer{}, ErrNotFound
	}
	if err == nil && strings.HasPrefix(peer.ProjectID, "repository:") {
		if _, validationErr := s.repositoryInfoForPeer(ctx, peer, sessionID); validationErr != nil {
			return Peer{}, validationErr
		}
	}
	return peer, err
}
