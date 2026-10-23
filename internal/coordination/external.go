package coordination

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// ExternalClaim/ExternalTransition are manual ingress controls, not remote
// worker leases. Any existing worker ownership requires its explicit release
// or recovery before manual processing. Callers hold admission then peer lock,
// which also fences AcquireLease; no check-then-mutate race is introduced.
func (s *Store) ExternalClaim(ctx context.Context, peerID string) (Message, error) {
	return s.claim(ctx, peerID, "", true)
}
func (s *Store) ExternalTransition(ctx context.Context, peerID, messageID string, revision int64, event string, payload json.RawMessage) (Message, error) {
	return s.transition(ctx, peerID, messageID, revision, event, payload, "", true)
}
func rejectExternalWorkerLease(ctx context.Context, tx *sql.Tx, peerID string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_leases WHERE peer_id=$1)`, peerID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrBusy
	}
	return nil
}

type externalIngressKey struct{}

func (s *Store) ExternalSend(ctx context.Context, peerID string, in SendInput) (Message, error) {
	return s.Send(context.WithValue(ctx, externalIngressKey{}, true), peerID, in)
}
func (s *Store) ExternalReply(ctx context.Context, peerID, messageID string, payload json.RawMessage, key string) (Message, error) {
	return s.Reply(context.WithValue(ctx, externalIngressKey{}, true), peerID, messageID, payload, key)
}

// RecoverExternal uses existing fenced recovery: live owners refuse admission,
// expired started work becomes blocked exactly once and is never replayed.
// An operator must stop local delivery before switching to manual processing.
func (s *Store) RecoverExternal(ctx context.Context, peerID string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockAdmission(ctx, tx, peerID); err != nil {
		return err
	}
	if _, err = lockPeer(ctx, tx, peerID); err != nil {
		return err
	}
	if err = checkScopedCredential(ctx, tx, peerID, true); err != nil {
		return err
	}
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT expires_at>clock_timestamp() FROM coordination_leases WHERE peer_id=$1 FOR UPDATE`, peerID).Scan(&held)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if held {
		return ErrBusy
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE to_peer=$1 AND state='started' ORDER BY id FOR UPDATE`, peerID)
	if err != nil {
		return err
	}
	var pending []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return e
		}
		pending = append(pending, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, m := range pending {
		m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='blocked',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, m.ID))
		if err != nil {
			return err
		}
		if err = appendEvent(ctx, tx, m, "blocked", json.RawMessage(`{"reason":"executor_interrupted","requires_review":true}`)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM coordination_leases WHERE peer_id=$1`, peerID); err != nil {
		return err
	}
	return commitPeerTransaction(ctx, tx, peerID)
}
