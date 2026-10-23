package coordination

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Peers exposes only enabled peers from the caller's project. Credentials and
// their hashes never enter this result or model-visible discovery metadata.
func (s *Store) Peers(ctx context.Context, peerID string) ([]Peer, error) {
	if err := checkScopedCredential(ctx, s.db, peerID, false); err != nil {
		return nil, err
	}
	if err := s.validateRepositoryPeerID(ctx, peerID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.project_id,p.name,p.role,p.workspace,p.capabilities,p.endpoint,p.enabled
		FROM coordination_peers p JOIN coordination_peers caller ON caller.id=$1 AND caller.enabled
		WHERE p.project_id=caller.project_id AND p.enabled ORDER BY p.name`, peerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	peers := make([]Peer, 0)
	for rows.Next() {
		peer, err := scanPeer(rows)
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, peer := range peers {
		if err := validateRepositoryPeer(ctx, s.db, peer); err != nil {
			return nil, err
		}
	}
	return peers, nil
}

func (s *Store) Reply(ctx context.Context, peerID, messageID string, payload json.RawMessage, key string) (Message, error) {
	original, err := s.Read(ctx, peerID, messageID)
	if err != nil {
		return Message{}, err
	}
	if original.To != peerID {
		return Message{}, ErrUnauthorized
	}
	peers, err := s.Peers(ctx, peerID)
	if err != nil {
		return Message{}, err
	}
	for _, peer := range peers {
		if peer.ID == original.From {
			return s.Send(ctx, peerID, SendInput{To: peer.Name, Kind: "answer", InReplyTo: original.ID, ConversationID: original.ConversationID, TaskID: original.TaskID, Payload: payload, IdempotencyKey: key})
		}
	}
	return Message{}, ErrUnauthorized
}

func (s *Store) MessageEvents(ctx context.Context, peerID, messageID string, afterRevision int64, limit int) ([]Event, error) {
	if afterRevision < 0 || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("coordination: nonnegative revision and limit 1..1000 required")
	}
	if _, err := s.Read(ctx, peerID, messageID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.project_id,e.sequence,e.message_id,e.revision,e.type,e.payload,e.created_at
		FROM coordination_events e JOIN coordination_messages m ON m.id=e.message_id
		JOIN coordination_peers p ON p.id=$1 AND p.enabled
		WHERE e.message_id=$2 AND (m.from_peer=p.id OR m.to_peer=p.id) AND e.revision>$3 ORDER BY e.revision LIMIT $4`, peerID, messageID, afterRevision, limit)
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

// Await waits for a newer durable revision or a terminal result. The caller
// supplies a bounded context; cancellation never claims or cancels the task.
func (s *Store) Await(ctx context.Context, peerID, messageID string, afterRevision int64) (Message, error) {
	if afterRevision < 0 {
		return Message{}, fmt.Errorf("coordination: revision must be nonnegative")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		m, err := s.Read(ctx, peerID, messageID)
		if err != nil {
			return Message{}, err
		}
		if m.Revision > afterRevision || m.State.Terminal() {
			return m, nil
		}
		select {
		case <-ctx.Done():
			return Message{}, ctx.Err()
		case <-ticker.C:
		}
	}
}
