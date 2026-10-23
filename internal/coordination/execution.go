package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type Binding struct {
	Peer          Peer
	SessionID     string
	validationErr error
}

// DispatchBindings pages only peers with outstanding work. The keyset stays
// stable while another worker finishes messages or creates new peer bindings.
func (s *Store) DispatchBindings(ctx context.Context, afterID string, limit int) ([]Binding, error) {
	if limit < 1 || limit > 500 {
		return nil, fmt.Errorf("coordination: binding page limit must be 1..500")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.project_id,p.name,p.role,p.workspace,p.capabilities,p.endpoint,p.enabled,b.session_id
		FROM coordination_peers p JOIN coordination_sessions b ON b.peer_id=p.id
		WHERE p.enabled AND p.id>$1 AND EXISTS(SELECT 1 FROM coordination_messages m WHERE m.to_peer=p.id
			AND m.state IN ('started','blocked','accepted'))
		ORDER BY p.id LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := make([]Binding, 0)
	for rows.Next() {
		var b Binding
		var capabilities []byte
		if err := rows.Scan(&b.Peer.ID, &b.Peer.ProjectID, &b.Peer.Name, &b.Peer.Role, &b.Peer.Workspace, &capabilities, &b.Peer.Endpoint, &b.Peer.Enabled, &b.SessionID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(capabilities, &b.Peer.Capabilities); err != nil {
			return nil, err
		}
		bindings = append(bindings, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range bindings {
		bindings[i].validationErr = validateRepositoryPeer(ctx, s.db, bindings[i].Peer)
	}
	return bindings, nil
}

// CompleteLease atomically finishes a claimed run and, for a request, accepts
// its correlated answer. Failure cannot publish an answer without completion.
// Notification messages finish without generating an endless reply cycle.
func (s *Store) CompleteLease(ctx context.Context, lease Lease, messageID string, payload json.RawMessage) (Message, error) {
	if err := validateLease(lease); err != nil {
		return Message{}, err
	}
	if len(payload) == 0 || len(payload) > 65536 || !json.Valid(payload) {
		return Message{}, fmt.Errorf("coordination: bounded JSON result required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	if err := lockAdmission(ctx, tx, lease.PeerID); err != nil {
		return Message{}, err
	}
	if _, err := enabledPeer(ctx, tx, lease.PeerID); err != nil {
		return Message{}, err
	}
	if err := checkLease(ctx, tx, lease.PeerID, lease.OwnerID); err != nil {
		return Message{}, err
	}
	// Lock both peer identities before the message, matching Send's lock order.
	// Otherwise sender cancellation can hold its peer lock while waiting for a
	// result whose reciprocal answer needs that same identity lock.
	var fromID string
	if err := tx.QueryRowContext(ctx, `SELECT from_peer FROM coordination_messages WHERE id=$1 AND to_peer=$2`, messageID, lease.PeerID).Scan(&fromID); err != nil {
		return Message{}, err
	}
	if _, err := enabledPeer(ctx, tx, fromID); err != nil {
		return Message{}, err
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1 AND to_peer=$2 FOR UPDATE`, messageID, lease.PeerID))
	if err != nil {
		return Message{}, err
	}
	if m.State == Completed {
		return m, nil
	} // The running model may already have used agent_reply.
	if m.State != Started {
		return Message{}, fmt.Errorf("coordination: cannot finish %s work", m.State)
	}
	if err := enforceRequestContracts(ctx, tx, m); err != nil {
		if errors.Is(err, ErrRequestContracts) {
			if e := tx.Commit(); e != nil {
				return Message{}, e
			}
		}
		return Message{}, err
	}
	if m.Kind == "request" {
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM coordination_peers WHERE id=$1 AND project_id=$2 AND enabled`, m.From, m.ProjectID).Scan(&name); err != nil {
			return Message{}, err
		}
		_, err = s.sendTx(ctx, tx, lease.PeerID, SendInput{To: name, Kind: "answer", ConversationID: m.ConversationID, TaskID: m.TaskID, InReplyTo: m.ID, Payload: payload, IdempotencyKey: "worker-answer:" + m.ID})
		if err != nil {
			return Message{}, err
		}
		m, err = scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1`, m.ID))
		if err != nil {
			return Message{}, err
		}
	} else {
		m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='completed',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, m.ID))
		if err != nil {
			return Message{}, err
		}
		if err := appendEvent(ctx, tx, m, "completed", payload); err != nil {
			return Message{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return m, nil
}

// failExecution reads the latest revision because a running agent may report
// progress. The worker's lease still fences its eventual terminal update.
func (s *Store) failExecution(ctx context.Context, lease Lease, messageID, eventType string, payload json.RawMessage) error {
	for range 3 {
		m, err := s.Read(ctx, lease.PeerID, messageID)
		if err != nil {
			return err
		}
		if m.State.Terminal() {
			return nil
		}
		_, err = s.WorkerTransition(ctx, lease, messageID, m.Revision, eventType, payload)
		if err == ErrConflict {
			continue
		}
		return err
	}
	return ErrConflict
}
