package coordination

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"
)

const (
	MinPeerCredentialTTL     = time.Minute
	MaxPeerCredentialTTL     = 30 * 24 * time.Hour
	DefaultPeerCredentialTTL = 24 * time.Hour
)

type CredentialMetadata struct {
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}
type credentialScopeKey struct{}
type credentialScope struct {
	actor string
	hash  [32]byte
}

// AuthenticateScoped binds only the authenticated actor and credential hash to
// request context. Token bytes never enter downstream metadata or errors.
func (s *Store) AuthenticateScoped(ctx context.Context, token string) (Peer, context.Context, error) {
	peer, err := s.Authenticate(ctx, token)
	if err != nil {
		return Peer{}, nil, err
	}
	scope := credentialScope{actor: peer.ID, hash: sha256.Sum256([]byte(token))}
	return peer, context.WithValue(ctx, credentialScopeKey{}, scope), nil
}

type credentialQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkScopedCredential(ctx context.Context, q credentialQuerier, peerID string, lock bool) error {
	scope, present := ctx.Value(credentialScopeKey{}).(credentialScope)
	if !present {
		return nil
	}
	if scope.actor != peerID {
		return ErrUnauthorized
	}
	query := `SELECT peer_id=$2 AND NOT revoked AND (expires_at IS NULL OR expires_at>clock_timestamp()) FROM coordination_credentials WHERE token_hash=$1`
	if lock {
		query += " FOR SHARE"
	}
	var valid bool
	if err := q.QueryRowContext(ctx, query, scope.hash[:], peerID).Scan(&valid); err != nil {
		if err == sql.ErrNoRows {
			return ErrUnauthorized
		}
		return err
	}
	if !valid {
		return ErrUnauthorized
	}
	return nil
}
func commitPeerTransaction(ctx context.Context, tx *sql.Tx, peerID string) error {
	// clock_timestamp rechecks expiry after lock/DDL/event waits. It is not the
	// transaction-start timestamp. Expired authorization rolls back all effects.
	if err := checkScopedCredential(ctx, tx, peerID, true); err != nil {
		return err
	}
	return tx.Commit()
}
func validateCredentialTTL(ttl time.Duration) error {
	if ttl < MinPeerCredentialTTL || ttl > MaxPeerCredentialTTL {
		return fmt.Errorf("coordination: peer credential TTL must be between 1 minute and 30 days")
	}
	return nil
}

// RevokePeerCredential is trusted operator administration, not peer ingress or
// a model tool. Retiring one key never disables the peer or its replacement.
func (s *Store) RevokePeerCredential(ctx context.Context, anchorSession, targetSession, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if len(token) != 64 {
		return fmt.Errorf("coordination: valid private credential required")
	}
	anchor, err := s.PeerForSession(ctx, anchorSession)
	if err != nil {
		return err
	}
	target, err := s.PeerForSession(ctx, targetSession)
	if err != nil {
		return err
	}
	if anchor.ProjectID != target.ProjectID {
		return ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockAdmission(ctx, tx, anchor.ID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM coordination_peers WHERE id IN ($1,$2) AND project_id=$3 AND enabled ORDER BY id FOR UPDATE`, anchor.ID, target.ID, anchor.ProjectID)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	expected := 2
	if anchor.ID == target.ID {
		expected = 1
	}
	if count != expected {
		return ErrUnauthorized
	}
	if err = validateRepositoryPeer(ctx, tx, anchor); err != nil {
		return err
	}
	if err = validateRepositoryPeer(ctx, tx, target); err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(token))
	result, err := tx.ExecContext(ctx, `UPDATE coordination_credentials SET revoked=true WHERE token_hash=$1 AND peer_id=$2`, hash[:], target.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}
