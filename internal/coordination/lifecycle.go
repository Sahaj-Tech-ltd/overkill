package coordination

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func enabledPeer(ctx context.Context, tx *sql.Tx, id string) (Peer, error) {
	peer, err := scanPeer(tx.QueryRowContext(ctx, `SELECT id,project_id,name,role,workspace,capabilities,endpoint,enabled FROM coordination_peers WHERE id=$1 AND enabled FOR SHARE`, id))
	if err == nil {
		err = validateRepositoryPeer(ctx, tx, peer)
	}
	return peer, err
}

// Read deliberately returns not-found for messages outside the authenticated
// peer's correspondence so arbitrary IDs reveal no other project's activity.
func (s *Store) Read(ctx context.Context, peerID, messageID string) (Message, error) {
	if err := checkScopedCredential(ctx, s.db, peerID, false); err != nil {
		return Message{}, err
	}
	if err := s.validateRepositoryPeerID(ctx, peerID); err != nil {
		return Message{}, err
	}
	return scanMessage(s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages
		WHERE id=$1 AND (from_peer=$2 OR to_peer=$2)
		AND EXISTS(SELECT 1 FROM coordination_peers WHERE id=$2 AND enabled)`, messageID, peerID))
}

// Claim atomically starts one pending message. Started messages are not
// automatically reclaimed after a crash: replaying a partially executed tool
// task could duplicate its side effects. Recovery inspects its durable state.
func (s *Store) Claim(ctx context.Context, peerID string) (Message, error) {
	return s.claim(ctx, peerID, "")
}

func (s *Store) ClaimLease(ctx context.Context, lease Lease) (Message, error) {
	if err := validateLease(lease); err != nil {
		return Message{}, err
	}
	return s.claim(ctx, lease.PeerID, lease.OwnerID)
}

func (s *Store) claim(ctx context.Context, peerID, ownerID string, external ...bool) (Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	if err := lockAdmission(ctx, tx, peerID); err != nil {
		return Message{}, err
	}
	peer, err := lockPeer(ctx, tx, peerID)
	if err != nil {
		return Message{}, err
	}
	if err = checkScopedCredential(ctx, tx, peerID, true); err != nil {
		return Message{}, err
	}
	if len(external) > 0 && external[0] {
		if err := rejectExternalWorkerLease(ctx, tx, peerID); err != nil {
			return Message{}, err
		}
	}
	if ownerID != "" {
		if err := checkLease(ctx, tx, peerID, ownerID); err != nil {
			return Message{}, err
		}
	}
	var busy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_messages WHERE to_peer=$1 AND state IN ('started','blocked'))`, peerID).Scan(&busy); err != nil {
		return Message{}, err
	}
	if busy {
		return Message{}, ErrBusy
	}
	// Expired, unstarted requests get a durable terminal status instead of
	// disappearing from claim queries while their UI still says accepted.
	expiredRows, err := tx.QueryContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages
		WHERE to_peer=$1 AND state='accepted' AND deadline<=clock_timestamp() ORDER BY created_at,id LIMIT 100 FOR UPDATE`, peerID)
	if err != nil {
		return Message{}, err
	}
	var expired []Message
	for expiredRows.Next() {
		m, err := scanMessage(expiredRows)
		if err != nil {
			expiredRows.Close()
			return Message{}, err
		}
		expired = append(expired, m)
	}
	if err := expiredRows.Err(); err != nil {
		expiredRows.Close()
		return Message{}, err
	}
	if err := expiredRows.Close(); err != nil {
		return Message{}, err
	}
	for _, message := range expired {
		message, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='failed',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, message.ID))
		if err != nil {
			return Message{}, err
		}
		if err := appendEvent(ctx, tx, message, "failed", json.RawMessage(`{"reason":"deadline_elapsed_before_start"}`)); err != nil {
			return Message{}, err
		}
	}
	failedDependencies, err := failDependencies(ctx, tx, peer)
	if err != nil {
		return Message{}, err
	}
	m, failedContracts, err := claimContractReady(ctx, tx, peer)
	if err != nil {
		if err == ErrNotFound && (len(expired) > 0 || failedDependencies > 0 || failedContracts > 0) {
			if commitErr := commitPeerTransaction(ctx, tx, peerID); commitErr != nil {
				return Message{}, commitErr
			}
		}
		return Message{}, err
	}
	if m.ExecutionAttempts >= MaxExecutionAttempts {
		if err := blockAttemptLimit(ctx, tx, peer, m); err != nil {
			return Message{}, err
		}
		if err := commitPeerTransaction(ctx, tx, peerID); err != nil {
			return Message{}, err
		}
		return Message{}, ErrRequestBudget
	}
	m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='started',revision=revision+1,execution_attempts=execution_attempts+1,updated_at=clock_timestamp()
		WHERE id=$1 RETURNING `+messageColumns, m.ID))
	if err != nil {
		return Message{}, err
	}
	if err := appendEvent(ctx, tx, m, "started", json.RawMessage(`{}`)); err != nil {
		return Message{}, err
	}
	if err := commitPeerTransaction(ctx, tx, peerID); err != nil {
		return Message{}, err
	}
	return m, nil
}

// Transition records status or progress together with its replayable event.
// Only the recipient reports work; either correspondent may cancel. Revisions
// prevent a delayed progress report from undoing completion or cancellation.
func (s *Store) Transition(ctx context.Context, peerID, messageID string, expectedRevision int64, eventType string, payload json.RawMessage) (Message, error) {
	return s.transition(ctx, peerID, messageID, expectedRevision, eventType, payload, "")
}

// WorkerTransition fences an expired executor even if it later produces output.
func (s *Store) WorkerTransition(ctx context.Context, lease Lease, messageID string, expectedRevision int64, eventType string, payload json.RawMessage) (Message, error) {
	if err := validateLease(lease); err != nil {
		return Message{}, err
	}
	return s.transition(ctx, lease.PeerID, messageID, expectedRevision, eventType, payload, lease.OwnerID)
}

func (s *Store) transition(ctx context.Context, peerID, messageID string, expectedRevision int64, eventType string, payload json.RawMessage, ownerID string, external ...bool) (Message, error) {
	if expectedRevision < 1 || len(payload) == 0 || len(payload) > 65536 || !json.Valid(payload) {
		return Message{}, fmt.Errorf("coordination: revision and bounded JSON payload are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	// Legacy explicit resume must serialize with claim, accounting and trusted
	// review before it can make another request in this task active.
	if err := lockAdmission(ctx, tx, peerID); err != nil {
		return Message{}, err
	}
	peer, err := lockPeer(ctx, tx, peerID)
	if err != nil {
		return Message{}, err
	}
	if err = checkScopedCredential(ctx, tx, peerID, true); err != nil {
		return Message{}, err
	}
	if len(external) > 0 && external[0] && eventType != "cancelled" {
		if err := rejectExternalWorkerLease(ctx, tx, peerID); err != nil {
			return Message{}, err
		}
	}
	if ownerID != "" {
		if err := checkLease(ctx, tx, peerID, ownerID); err != nil {
			return Message{}, err
		}
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages
		WHERE id=$1 AND project_id=$2 AND (from_peer=$3 OR to_peer=$3) FOR UPDATE`, messageID, peer.ProjectID, peer.ID))
	if err != nil {
		return Message{}, err
	}
	if m.Revision != expectedRevision {
		return Message{}, ErrConflict
	}
	if m.State.Terminal() {
		return Message{}, fmt.Errorf("coordination: message is already %s", m.State)
	}
	next := m.State
	if eventType == "cancelled" {
		next = Cancelled
	} else {
		if peer.ID != m.To {
			return Message{}, ErrUnauthorized
		}
		switch eventType {
		case "started":
			if m.State != Blocked {
				return Message{}, fmt.Errorf("coordination: only a blocked message can resume")
			}
			var busy bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_messages WHERE to_peer=$1 AND state='started' AND id<>$2)`, peerID, messageID).Scan(&busy); err != nil {
				return Message{}, err
			}
			if busy {
				return Message{}, ErrBusy
			}
			if m.ExecutionAttempts >= MaxExecutionAttempts {
				if err := blockAttemptLimit(ctx, tx, peer, m); err != nil {
					return Message{}, err
				}
				if err := commitPeerTransaction(ctx, tx, peerID); err != nil {
					return Message{}, err
				}
				return Message{}, ErrRequestBudget
			}
			next = Started
		case "progress", "contract_changed":
			if m.State != Started {
				return Message{}, fmt.Errorf("coordination: progress requires started state")
			}
		case "blocked":
			if m.State != Started {
				return Message{}, fmt.Errorf("coordination: only started work can block")
			}
			next = Blocked
		case "completed", "failed":
			if m.State != Started && m.State != Blocked {
				return Message{}, fmt.Errorf("coordination: result requires claimed work")
			}
			next = State(eventType)
		default:
			return Message{}, fmt.Errorf("coordination: unsupported transition %q", eventType)
		}
	}
	if eventType == "started" || eventType == "completed" {
		if err := enforceRequestContracts(ctx, tx, m); err != nil {
			if errors.Is(err, ErrRequestContracts) {
				if e := commitPeerTransaction(ctx, tx, peerID); e != nil {
					return Message{}, e
				}
			}
			return Message{}, err
		}
	}
	increment := 0
	if eventType == "started" {
		increment = 1
	}
	m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state=$1,revision=revision+1,execution_attempts=execution_attempts+$3,updated_at=clock_timestamp()
		WHERE id=$2 RETURNING `+messageColumns, next, m.ID, increment))
	if err != nil {
		return Message{}, err
	}
	if err := appendEvent(ctx, tx, m, eventType, payload); err != nil {
		return Message{}, err
	}
	if err := commitPeerTransaction(ctx, tx, peerID); err != nil {
		return Message{}, err
	}
	return m, nil
}

// Inbox includes started/blocked work so a restarted worker can present an
// interrupted task for recovery instead of silently executing it again.
func (s *Store) Inbox(ctx context.Context, peerID string, limit int) ([]Message, error) {
	if err := checkScopedCredential(ctx, s.db, peerID, false); err != nil {
		return nil, err
	}
	if err := s.validateRepositoryPeerID(ctx, peerID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("coordination: limit must be 1..200")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages
		WHERE to_peer=$1 AND state IN ('accepted','started','blocked')
		AND EXISTS(SELECT 1 FROM coordination_peers WHERE id=$1 AND enabled)
		ORDER BY created_at,id LIMIT $2`, peerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Message, 0)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// Events replays only this peer's correspondence after a project cursor.
// A cursor remains valid over restart and does not acknowledge execution.
func (s *Store) Events(ctx context.Context, peerID string, after int64, limit int) ([]Event, error) {
	if err := s.validateRepositoryPeerID(ctx, peerID); err != nil {
		return nil, err
	}
	if after < 0 || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("coordination: nonnegative cursor and limit 1..1000 required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.project_id,e.sequence,e.message_id,e.revision,e.type,e.payload,e.created_at
		FROM coordination_events e JOIN coordination_messages m ON m.id=e.message_id
		JOIN coordination_peers p ON p.id=$1 AND p.enabled AND p.project_id=e.project_id
		WHERE (m.from_peer=p.id OR m.to_peer=p.id) AND e.sequence>$2
		ORDER BY e.sequence LIMIT $3`, peerID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ProjectID, &event.Sequence, &event.MessageID, &event.Revision, &event.Type, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// DisablePeer revokes every credential and prevents further reads/changes.
// This is an application/admin operation, not a model-visible operation.
func (s *Store) DisablePeer(ctx context.Context, peerID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE coordination_peers SET enabled=false WHERE id=$1`, peerID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `UPDATE coordination_credentials SET revoked=true WHERE peer_id=$1`, peerID); err != nil {
		return err
	}
	return tx.Commit()
}

// PendingCount can drive application wakeups without claiming or replaying work.
func (s *Store) PendingCount(ctx context.Context, peerID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coordination_messages
		WHERE to_peer=$1 AND state='accepted' AND deadline>NOW()
		AND EXISTS(SELECT 1 FROM coordination_peers WHERE id=$1 AND enabled)`, peerID).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return count, err
}
