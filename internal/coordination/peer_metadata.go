package coordination

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// ErrPeerCredentialUnconfirmed preserves the delivered private file when commit
// acknowledgement is uncertain. Operators can inspect/revoke without losing it.
var ErrPeerCredentialUnconfirmed = errors.New("coordination: credential commit unconfirmed; retain private credential file for inspection")

// ValidatePeerEndpoint validates advertised ingress metadata. Discovery never
// dials this value; an outbound caller must independently apply URL policy.
func ValidatePeerEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 2048 || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("coordination: invalid peer endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && !strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("coordination: HTTP peer endpoint requires loopback")
	}
	return nil
}

// ConfigureSessionPeer is a trusted operator control, never a model tool. Both
// identities must already have validated persisted session bindings in the same
// project. Delivery of the new credential must succeed before its hash commits.
// The callback must keep the credential private and remove its file on error.
func (s *Store) ConfigureSessionPeer(ctx context.Context, anchorSession, targetSession, endpoint string, capabilities []string, deliver func(string) error) (Peer, error) {
	peer, _, err := s.configureSessionPeer(ctx, anchorSession, targetSession, endpoint, capabilities, 0, deliver)
	return peer, err
}

// ConfigureSessionPeerWithTTL is trusted provisioning. TTL uses database time;
// legacy credentials remain NULL-expiry, never silently shortened or deleted.
func (s *Store) ConfigureSessionPeerWithTTL(ctx context.Context, anchorSession, targetSession, endpoint string, capabilities []string, ttl time.Duration, deliver func(string) error) (Peer, CredentialMetadata, error) {
	if err := validateCredentialTTL(ttl); err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	return s.configureSessionPeer(ctx, anchorSession, targetSession, endpoint, capabilities, ttl, deliver)
}
func (s *Store) configureSessionPeer(ctx context.Context, anchorSession, targetSession, endpoint string, capabilities []string, ttl time.Duration, deliver func(string) error) (Peer, CredentialMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ValidatePeerEndpoint(endpoint); err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	if len(capabilities) > 64 || deliver == nil {
		return Peer{}, CredentialMetadata{}, fmt.Errorf("coordination: bounded capabilities and private credential destination required")
	}
	for _, v := range capabilities {
		if strings.TrimSpace(v) == "" || len(v) > 128 {
			return Peer{}, CredentialMetadata{}, fmt.Errorf("coordination: invalid capability")
		}
	}
	anchor, err := s.PeerForSession(ctx, anchorSession)
	if err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	target, err := s.PeerForSession(ctx, targetSession)
	if err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	if anchor.ProjectID != target.ProjectID {
		return Peer{}, CredentialMetadata{}, ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	defer tx.Rollback()
	// Match admission order and fence concurrent disabling/rebinding. Repository
	// identity/workspace validation is repeated within the authoritative snapshot.
	if err = lockAdmission(ctx, tx, anchor.ID); err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	// Lock both identities in deterministic order. Concurrent revocation must
	// finish before we can mint a credential; disabling afterwards revokes it.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM coordination_peers WHERE id IN ($1,$2) AND enabled AND project_id=$3 ORDER BY id FOR UPDATE`, anchor.ID, target.ID, anchor.ProjectID)
	if err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	count := 0
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return Peer{}, CredentialMetadata{}, err
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	expected := 2
	if anchor.ID == target.ID {
		expected = 1
	}
	if count != expected {
		return Peer{}, CredentialMetadata{}, ErrUnauthorized
	}
	for _, binding := range []struct {
		session string
		peer    Peer
	}{{anchorSession, anchor}, {targetSession, target}} {
		var folder string
		if err = tx.QueryRowContext(ctx, `SELECT folder FROM sessions WHERE id=$1`, binding.session).Scan(&folder); err != nil {
			return Peer{}, CredentialMetadata{}, ErrUnauthorized
		}
		observed, e := preparePeer(Peer{ProjectID: binding.peer.ProjectID, Name: binding.peer.Name, Role: binding.peer.Role, Workspace: folder})
		if e != nil || observed.Workspace != binding.peer.Workspace {
			return Peer{}, CredentialMetadata{}, ErrUnauthorized
		}
	}
	if err = validateRepositoryPeer(ctx, tx, anchor); err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	if err = validateRepositoryPeer(ctx, tx, target); err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	if capabilities == nil {
		capabilities = []string{}
	}
	raw, err := json.Marshal(capabilities)
	if err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE coordination_peers SET endpoint=$2,capabilities=$3 WHERE id=$1 AND enabled`, target.ID, endpoint, raw)
	if err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	if n != 1 {
		return Peer{}, CredentialMetadata{}, ErrUnauthorized
	}
	var secret [32]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return Peer{}, CredentialMetadata{}, fmt.Errorf("coordination: credential generation failed")
	}
	token := hex.EncodeToString(secret[:])
	hash := sha256.Sum256([]byte(token))
	var expires sql.NullTime
	if err = tx.QueryRowContext(ctx, `INSERT INTO coordination_credentials(token_hash,peer_id,expires_at) VALUES($1,$2,CASE WHEN $3::bigint>0 THEN clock_timestamp()+$3::bigint*interval '1 millisecond' ELSE NULL END) RETURNING expires_at`, hash[:], target.ID, ttl.Milliseconds()).Scan(&expires); err != nil {
		return Peer{}, CredentialMetadata{}, err
	}
	if err = deliver(token); err != nil {
		return Peer{}, CredentialMetadata{}, fmt.Errorf("coordination: private credential delivery failed")
	}
	if err = tx.Commit(); err != nil {
		return Peer{}, CredentialMetadata{}, ErrPeerCredentialUnconfirmed
	}
	target.Endpoint = endpoint
	target.Capabilities = capabilities
	var metadata CredentialMetadata
	if expires.Valid {
		value := expires.Time.UTC()
		metadata.ExpiresAt = &value
	}
	return target, metadata, nil
}
