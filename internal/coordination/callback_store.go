package coordination

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var callbackDDL = []string{
	`CREATE TABLE IF NOT EXISTS coordination_callback_subscriptions (
 id TEXT PRIMARY KEY, peer_id TEXT NOT NULL REFERENCES coordination_peers(id), destination TEXT NOT NULL,
 cursor BIGINT NOT NULL CHECK(cursor>=0), active BOOLEAN NOT NULL DEFAULT true, secret BYTEA NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	`ALTER TABLE coordination_callback_subscriptions DROP CONSTRAINT IF EXISTS coordination_callback_subscriptions_peer_id_destination_key`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_coordination_callbacks_active_destination ON coordination_callback_subscriptions(peer_id,destination) WHERE active`,
	`ALTER TABLE coordination_callback_subscriptions ADD COLUMN IF NOT EXISTS signing_generation BIGINT NOT NULL DEFAULT 1`,
	`ALTER TABLE coordination_callback_subscriptions ADD COLUMN IF NOT EXISTS last_attempt TIMESTAMPTZ NOT NULL DEFAULT 'epoch'`,
	`CREATE TABLE IF NOT EXISTS coordination_callback_deliveries (
 subscription_id TEXT NOT NULL REFERENCES coordination_callback_subscriptions(id), sequence BIGINT NOT NULL,
 body BYTEA NOT NULL, state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','delivered','dead','cancelled')),
 attempts INT NOT NULL DEFAULT 0, next_attempt TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 lease_owner TEXT NOT NULL DEFAULT '', lease_until TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 status_code INT NOT NULL DEFAULT 0, failure TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(subscription_id,sequence))`,
	`CREATE INDEX IF NOT EXISTS idx_coordination_callbacks_pending ON coordination_callback_deliveries(subscription_id,state,next_attempt,lease_until) WHERE state!='delivered'`,
}

type CallbackSubscription struct {
	ID          string `json:"id"`
	Destination string `json:"destination"`
	Cursor      int64  `json:"cursor"`
	Active      bool   `json:"active"`
}
type CallbackDelivery struct {
	Sequence    int64     `json:"sequence"`
	State       string    `json:"state"`
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt"`
	StatusCode  int       `json:"status_code"`
	Failure     string    `json:"failure"`
}

func (s *Store) SubscribeCallback(ctx context.Context, peerID, destination string, after int64) (CallbackSubscription, string, error) {
	if !destinationName.MatchString(destination) || after < 0 {
		return CallbackSubscription{}, "", fmt.Errorf("callbacks: valid destination and nonnegative cursor required")
	}
	// Authorization occurs before random generation and never exposes another peer's secret.
	var valid bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_peers WHERE id=$1 AND enabled)`, peerID).Scan(&valid); err != nil {
		return CallbackSubscription{}, "", err
	}
	if !valid {
		return CallbackSubscription{}, "", ErrUnauthorized
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return CallbackSubscription{}, "", err
	}
	sub := CallbackSubscription{ID: uuid.NewString(), Destination: destination, Cursor: after, Active: true}
	result, err := s.db.ExecContext(ctx, `INSERT INTO coordination_callback_subscriptions(id,peer_id,destination,cursor,secret) VALUES($1,$2,$3,$4,$5) ON CONFLICT(peer_id,destination) WHERE active DO NOTHING`, sub.ID, peerID, destination, after, secret)
	if err != nil {
		return CallbackSubscription{}, "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return CallbackSubscription{}, "", err
	}
	if n != 1 {
		return CallbackSubscription{}, "", ErrConflict
	}
	return sub, hex.EncodeToString(secret), nil
}
func (s *Store) CallbackSubscriptions(ctx context.Context, peerID string) ([]CallbackSubscription, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.destination,s.cursor,s.active FROM coordination_callback_subscriptions s JOIN coordination_peers p ON p.id=s.peer_id AND p.enabled WHERE s.peer_id=$1 ORDER BY s.active DESC,s.created_at DESC,s.id LIMIT 100`, peerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]CallbackSubscription, 0)
	for rows.Next() {
		var sub CallbackSubscription
		if err := rows.Scan(&sub.ID, &sub.Destination, &sub.Cursor, &sub.Active); err != nil {
			return nil, err
		}
		result = append(result, sub)
	}
	return result, rows.Err()
}
func (s *Store) callbackAccess(ctx context.Context, peerID, id string) error {
	var valid bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_callback_subscriptions s JOIN coordination_peers p ON p.id=s.peer_id AND p.enabled WHERE s.id=$1 AND s.peer_id=$2)`, id, peerID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrNotFound
	}
	return nil
}
func (s *Store) CallbackDeliveries(ctx context.Context, peerID, id string) ([]CallbackDelivery, error) {
	if err := s.callbackAccess(ctx, peerID, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,state,attempts,next_attempt,status_code,failure FROM coordination_callback_deliveries WHERE subscription_id=$1 ORDER BY sequence DESC LIMIT 100`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]CallbackDelivery, 0)
	for rows.Next() {
		var d CallbackDelivery
		if err := rows.Scan(&d.Sequence, &d.State, &d.Attempts, &d.NextAttempt, &d.StatusCode, &d.Failure); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// CancelCallback revokes delivery; it never cancels the underlying agent work.
func (s *Store) CancelCallback(ctx context.Context, peerID, id string) error {
	if err := s.callbackAccess(ctx, peerID, id); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE coordination_callback_subscriptions SET active=false WHERE id=$1 AND peer_id=$2`, id, peerID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE coordination_callback_deliveries SET state='cancelled',failure='subscription_cancelled',updated_at=NOW() WHERE subscription_id=$1 AND state='pending'`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RetryCallback explicitly resumes a dead-letter with the same stable identity.
func (s *Store) RetryCallback(ctx context.Context, peerID, id string, sequence int64) error {
	if err := s.callbackAccess(ctx, peerID, id); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE coordination_callback_deliveries SET state='pending',attempts=0,next_attempt=NOW(),failure='',lease_owner='',lease_until=NOW() WHERE subscription_id=$1 AND sequence=$2 AND state='dead' AND EXISTS(SELECT 1 FROM coordination_callback_subscriptions WHERE id=$1 AND active)`, id, sequence)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

type callbackClaim struct {
	id, destination, owner string
	generation             int64
	sequence               int64
	body, secret           []byte
	attempts               int
}

func (s *Store) claimCallback(ctx context.Context, owner string) (*callbackClaim, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var peer, project string
	c := &callbackClaim{owner: owner}
	err = tx.QueryRowContext(ctx, `SELECT s.id,s.destination,s.secret,s.signing_generation,s.peer_id,p.project_id FROM coordination_callback_subscriptions s JOIN coordination_peers p ON p.id=s.peer_id AND p.enabled WHERE s.active
 AND NOT EXISTS(SELECT 1 FROM coordination_callback_deliveries d WHERE d.subscription_id=s.id AND d.state!='delivered' AND (d.state='dead' OR d.next_attempt>NOW() OR d.lease_until>NOW()))
 AND EXISTS(SELECT 1 FROM coordination_events e JOIN coordination_messages m ON m.id=e.message_id WHERE e.project_id=p.project_id AND e.sequence>s.cursor AND (m.from_peer=p.id OR m.to_peer=p.id))
 ORDER BY s.last_attempt,s.created_at,s.id FOR UPDATE OF s SKIP LOCKED LIMIT 1`).Scan(&c.id, &c.destination, &c.secret, &c.generation, &peer, &project)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var e Event
	err = tx.QueryRowContext(ctx, `SELECT e.project_id,e.sequence,e.message_id,e.revision,e.type,e.payload,e.created_at FROM coordination_events e JOIN coordination_messages m ON m.id=e.message_id JOIN coordination_callback_subscriptions s ON s.id=$1 WHERE e.project_id=$2 AND e.sequence>s.cursor AND (m.from_peer=$3 OR m.to_peer=$3) ORDER BY e.sequence LIMIT 1`, c.id, project, peer).Scan(&e.ProjectID, &e.Sequence, &e.MessageID, &e.Revision, &e.Type, &e.Payload, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	c.sequence = e.Sequence
	body, err := json.Marshal(struct {
		ID    string `json:"delivery_id"`
		Event Event  `json:"event"`
	}{fmt.Sprintf("%s:%d", c.id, c.sequence), e})
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO coordination_callback_deliveries(subscription_id,sequence,body) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, c.id, c.sequence, body)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE coordination_callback_deliveries SET lease_owner=$3,lease_until=NOW()+INTERVAL '30 seconds',attempts=attempts+1,updated_at=NOW() WHERE subscription_id=$1 AND sequence=$2 AND state='pending' AND lease_until<=NOW() RETURNING body,attempts`, c.id, c.sequence, owner).Scan(&c.body, &c.attempts)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE coordination_callback_subscriptions SET last_attempt=NOW() WHERE id=$1`, c.id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return c, nil
}
func (s *Store) callbackOwned(ctx context.Context, c *callbackClaim) (bool, error) {
	var owned bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_callback_subscriptions s JOIN coordination_callback_deliveries d ON d.subscription_id=s.id JOIN coordination_peers p ON p.id=s.peer_id AND p.enabled WHERE s.id=$1 AND s.active AND d.sequence=$2 AND d.state='pending' AND d.lease_owner=$3 AND d.lease_until>NOW() AND s.signing_generation=$4)`, c.id, c.sequence, c.owner, c.generation).Scan(&owned)
	return owned, err
}
func (s *Store) finishCallback(ctx context.Context, c *callbackClaim, status int, failure string, dead, interrupted bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active, enabled bool
	var generation int64
	// Serialize acknowledgement with peer revocation as well as subscription
	// cancellation/rotation. A stale HTTP success cannot advance a revoked peer.
	err = tx.QueryRowContext(ctx, `SELECT s.active,s.signing_generation,p.enabled FROM coordination_callback_subscriptions s JOIN coordination_peers p ON p.id=s.peer_id WHERE s.id=$1 FOR UPDATE OF s,p`, c.id).Scan(&active, &generation, &enabled)
	if err != nil {
		return err
	}
	if !enabled || generation != c.generation {
		return ErrLeaseLost
	}
	state := "pending"
	if !active {
		state = "cancelled"
	} else if status >= 200 && status < 300 && active && !interrupted {
		state = "delivered"
	} else if dead {
		state = "dead"
	}
	delay := time.Duration(1<<min(c.attempts-1, 8)) * time.Second
	if interrupted {
		delay = 0
	}
	result, err := tx.ExecContext(ctx, `UPDATE coordination_callback_deliveries SET state=$4,status_code=$5,failure=$6,next_attempt=NOW()+$7*INTERVAL '1 second',lease_owner='',lease_until=NOW(),updated_at=NOW() WHERE subscription_id=$1 AND sequence=$2 AND lease_owner=$3 AND lease_until>NOW()`, c.id, c.sequence, c.owner, state, status, failure, delay.Seconds())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	if state == "delivered" {
		_, err = tx.ExecContext(ctx, `UPDATE coordination_callback_subscriptions SET cursor=$2 WHERE id=$1 AND active AND cursor<$2`, c.id, c.sequence)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RotateCallbackKey recovers a lost setup response without replaying agent work.
// Generation fencing prevents an old in-flight worker from acknowledging after
// rotation; retries preserve the exact delivery ID and body. Receivers should
// install the new secret then explicitly retry any authentication dead-letter.
func (s *Store) RotateCallbackKey(ctx context.Context, peerID, id string) (string, error) {
	if err := s.callbackAccess(ctx, peerID, id); err != nil {
		return "", err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE coordination_callback_subscriptions SET secret=$3,signing_generation=signing_generation+1 WHERE id=$1 AND peer_id=$2 AND active`, id, peerID, secret)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrConflict
	}
	// Fences old owners immediately and lets a new claim use the current key.
	if _, err := tx.ExecContext(ctx, `UPDATE coordination_callback_deliveries SET lease_owner='',lease_until=NOW(),next_attempt=NOW(),updated_at=NOW() WHERE subscription_id=$1 AND state='pending'`, id); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return hex.EncodeToString(secret), nil
}
