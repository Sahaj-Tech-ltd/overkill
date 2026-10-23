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
	"time"

	"github.com/google/uuid"
)

type Lease struct {
	transportCapability string
	PeerID              string
	OwnerID             string
	ExpiresAt           time.Time
}

func validateLease(lease Lease) error {
	if _, err := uuid.Parse(lease.PeerID); err != nil {
		return ErrLeaseLost
	}
	if _, err := uuid.Parse(lease.OwnerID); err != nil {
		return ErrLeaseLost
	}
	return nil
}

func lockPeer(ctx context.Context, tx *sql.Tx, id string) (Peer, error) {
	peer, err := scanPeer(tx.QueryRowContext(ctx, `SELECT id,project_id,name,role,workspace,capabilities,endpoint,enabled FROM coordination_peers WHERE id=$1 AND enabled FOR UPDATE`, id))
	if err == nil {
		err = validateRepositoryPeer(ctx, tx, peer)
	}
	return peer, err
}

// AcquireLease serializes one peer's executor across processes. Expired work
// becomes blocked for explicit recovery; acquiring never replays a started run.
func (s *Store) AcquireLease(ctx context.Context, peerID, ownerID string, ttl time.Duration) (Lease, error) {
	if _, err := uuid.Parse(ownerID); err != nil {
		return Lease{}, fmt.Errorf("coordination: worker owner must be a UUID")
	}
	if ttl < 300*time.Millisecond || ttl > time.Minute {
		return Lease{}, fmt.Errorf("coordination: lease TTL must be 300ms..1m")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Lease{}, err
	}
	defer tx.Rollback()
	if _, err := lockPeer(ctx, tx, peerID); err != nil {
		return Lease{}, err
	}
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_leases WHERE peer_id=$1 AND expires_at>clock_timestamp())`, peerID).Scan(&held)
	if err != nil {
		return Lease{}, err
	}
	if held {
		return Lease{}, ErrBusy
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE to_peer=$1 AND state='started' ORDER BY id FOR UPDATE`, peerID)
	if err != nil {
		return Lease{}, err
	}
	var interrupted []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			return Lease{}, err
		}
		interrupted = append(interrupted, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Lease{}, err
	}
	if err := rows.Close(); err != nil {
		return Lease{}, err
	}
	for _, m := range interrupted {
		m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='blocked',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, m.ID))
		if err != nil {
			return Lease{}, err
		}
		if err := appendEvent(ctx, tx, m, "blocked", json.RawMessage(`{"reason":"executor_interrupted","requires_review":true}`)); err != nil {
			return Lease{}, err
		}
	}
	var blocked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_messages WHERE to_peer=$1 AND state='blocked')`, peerID).Scan(&blocked); err != nil {
		return Lease{}, err
	}
	if blocked {
		if _, err := tx.ExecContext(ctx, `DELETE FROM coordination_leases WHERE peer_id=$1`, peerID); err != nil {
			return Lease{}, err
		}
		if err := tx.Commit(); err != nil {
			return Lease{}, err
		}
		return Lease{}, ErrRecoveryRequired
	}
	var capability [32]byte
	if _, err = rand.Read(capability[:]); err != nil {
		return Lease{}, err
	}
	secret := hex.EncodeToString(capability[:])
	hash := sha256.Sum256([]byte(secret))
	lease := Lease{PeerID: peerID, OwnerID: ownerID, transportCapability: secret}
	err = tx.QueryRowContext(ctx, `INSERT INTO coordination_leases(peer_id,owner_id,expires_at,transport_hash)
		VALUES($1,$2,clock_timestamp()+$3::bigint*interval '1 millisecond',$4)
		ON CONFLICT(peer_id) DO UPDATE SET owner_id=EXCLUDED.owner_id,expires_at=EXCLUDED.expires_at,transport_hash=EXCLUDED.transport_hash
		RETURNING expires_at`, peerID, ownerID, ttl.Milliseconds(), hash[:]).Scan(&lease.ExpiresAt)
	if err != nil {
		return Lease{}, err
	}
	if err := tx.Commit(); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

func (s *Store) RenewLease(ctx context.Context, lease Lease, ttl time.Duration) (Lease, error) {
	if err := validateLease(lease); err != nil {
		return Lease{}, err
	}
	if ttl < 300*time.Millisecond || ttl > time.Minute {
		return Lease{}, fmt.Errorf("coordination: lease TTL must be 300ms..1m")
	}
	err := s.db.QueryRowContext(ctx, `UPDATE coordination_leases SET expires_at=clock_timestamp()+$3::bigint*interval '1 millisecond'
		WHERE peer_id=$1 AND owner_id=$2 AND expires_at>clock_timestamp()
		AND EXISTS(SELECT 1 FROM coordination_peers WHERE id=$1 AND enabled) RETURNING expires_at`, lease.PeerID, lease.OwnerID, ttl.Milliseconds()).Scan(&lease.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{}, ErrLeaseLost
	}
	return lease, err
}

func (s *Store) ReleaseLease(ctx context.Context, lease Lease) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM coordination_leases WHERE peer_id=$1 AND owner_id=$2`, lease.PeerID, lease.OwnerID)
	return err
}

func checkLease(ctx context.Context, tx *sql.Tx, peerID, ownerID string) error {
	var matches bool
	err := tx.QueryRowContext(ctx, `SELECT owner_id=$2 AND expires_at>clock_timestamp() FROM coordination_leases WHERE peer_id=$1 FOR SHARE`, peerID, ownerID).Scan(&matches)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if !matches {
		return ErrLeaseLost
	}
	return nil
}
