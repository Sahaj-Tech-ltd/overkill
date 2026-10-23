package coordination

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// SessionTransport is trusted application configuration. CredentialFile is a
// private local reference and must never be returned in peer/model metadata.
type SessionTransport struct {
	SessionID      string
	PeerID         string
	ProjectID      string
	Workspace      string
	Endpoint       string
	CredentialFile string `json:"-"`
	Revision       int64
}

var transportDDL = []string{`CREATE TABLE IF NOT EXISTS coordination_session_transports(session_id TEXT PRIMARY KEY REFERENCES coordination_sessions(session_id) ON DELETE CASCADE,peer_id TEXT NOT NULL REFERENCES coordination_peers(id),project_id TEXT NOT NULL,workspace TEXT NOT NULL,endpoint TEXT NOT NULL,credential_file TEXT NOT NULL,revision BIGINT NOT NULL DEFAULT 1)`, `ALTER TABLE coordination_leases ADD COLUMN IF NOT EXISTS transport_hash BYTEA`}

func (s *Store) SessionTransport(ctx context.Context, sessionID string) (SessionTransport, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var b SessionTransport
	err := s.db.QueryRowContext(ctx, `SELECT session_id,peer_id,project_id,workspace,endpoint,credential_file,revision FROM coordination_session_transports WHERE session_id=$1`, sessionID).Scan(&b.SessionID, &b.PeerID, &b.ProjectID, &b.Workspace, &b.Endpoint, &b.CredentialFile, &b.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionTransport{SessionID: sessionID}, nil
	}
	return b, err
}

// BindSessionTransport requires an already verified same-hub authenticated peer
// snapshot. Application caller owns session admission and rebuilds its cache.
func (s *Store) BindSessionTransport(ctx context.Context, sessionID string, remote Peer, endpoint, credentialFile string) (SessionTransport, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if endpoint == "" || ValidatePeerEndpoint(endpoint) != nil || !filepath.IsAbs(credentialFile) || len(credentialFile) > 4096 {
		return SessionTransport{}, fmt.Errorf("coordination: valid explicit endpoint/private credential reference required")
	}
	local, err := s.PeerForSession(ctx, sessionID)
	if err != nil {
		return SessionTransport{}, err
	}
	if local.ID != remote.ID || local.ProjectID != remote.ProjectID || local.Workspace != remote.Workspace {
		return SessionTransport{}, ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SessionTransport{}, err
	}
	defer tx.Rollback()
	if err = lockAdmission(ctx, tx, local.ID); err != nil {
		return SessionTransport{}, err
	}
	current, err := lockPeer(ctx, tx, local.ID)
	if err != nil {
		return SessionTransport{}, err
	}
	if current.ProjectID != remote.ProjectID || current.Workspace != remote.Workspace {
		return SessionTransport{}, ErrUnauthorized
	}
	if err = rejectExternalWorkerLease(ctx, tx, local.ID); err != nil {
		return SessionTransport{}, err
	}
	var binding SessionTransport
	err = tx.QueryRowContext(ctx, `INSERT INTO coordination_session_transports(session_id,peer_id,project_id,workspace,endpoint,credential_file) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(session_id) DO UPDATE SET peer_id=EXCLUDED.peer_id,project_id=EXCLUDED.project_id,workspace=EXCLUDED.workspace,endpoint=EXCLUDED.endpoint,credential_file=EXCLUDED.credential_file,revision=coordination_session_transports.revision+1 RETURNING session_id,peer_id,project_id,workspace,endpoint,credential_file,revision`, sessionID, local.ID, local.ProjectID, local.Workspace, endpoint, credentialFile).Scan(&binding.SessionID, &binding.PeerID, &binding.ProjectID, &binding.Workspace, &binding.Endpoint, &binding.CredentialFile, &binding.Revision)
	if err != nil {
		return SessionTransport{}, err
	}
	if err = tx.Commit(); err != nil {
		return SessionTransport{}, err
	}
	return binding, nil
}

func (s *Store) ClearSessionTransport(ctx context.Context, sessionID string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	peer, err := s.PeerForSession(ctx, sessionID)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockAdmission(ctx, tx, peer.ID); err != nil {
		return err
	}
	if _, err = lockPeer(ctx, tx, peer.ID); err != nil {
		return err
	}
	if err = rejectExternalWorkerLease(ctx, tx, peer.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM coordination_session_transports WHERE session_id=$1 AND peer_id=$2`, sessionID, peer.ID); err != nil {
		return err
	}
	return tx.Commit()
}
