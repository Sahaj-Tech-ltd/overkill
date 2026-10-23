package coordination

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RuntimePresence describes an observed application runtime, not a client
// connection, delivery guarantee, or exclusive execution lease.
type RuntimePresence struct {
	State      string     `json:"state"`
	Source     string     `json:"source"`
	Observers  int        `json:"observers"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type RuntimeObservation struct {
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

const RuntimePresenceTTL = 20 * time.Second

var presenceDDL = []string{
	`CREATE TABLE IF NOT EXISTS coordination_runtime_presence (
 observer_id TEXT NOT NULL, peer_id TEXT NOT NULL REFERENCES coordination_peers(id) ON DELETE CASCADE,
 state TEXT NOT NULL CHECK(state IN ('ready','working','waiting_for_input','busy')),
 observed_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(observer_id,peer_id))`,
	`CREATE INDEX IF NOT EXISTS coordination_runtime_presence_expiry ON coordination_runtime_presence(expires_at)`,
}

// PublishRuntimePresence is application-only. Models and RPC clients cannot
// supply observations. The caller serializes snapshots for its unique observer.
// Snapshot replacement and expiry cleanup commit together using database time.
func (s *Store) PublishRuntimePresence(ctx context.Context, observer string, observations []RuntimeObservation) error {
	if _, err := uuid.Parse(observer); err != nil || len(observations) > 10000 {
		return fmt.Errorf("coordination: invalid runtime observer or snapshot")
	}
	seen := map[string]bool{}
	for _, observation := range observations {
		if _, err := uuid.Parse(observation.SessionID); err != nil || seen[observation.SessionID] {
			return fmt.Errorf("coordination: invalid runtime session")
		}
		seen[observation.SessionID] = true
		switch observation.State {
		case "ready", "working", "waiting_for_input", "busy":
		default:
			return fmt.Errorf("coordination: invalid runtime state")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':presence:' || $1,0))`, observer); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM coordination_runtime_presence WHERE observer_id=$1 OR expires_at<=clock_timestamp()`, observer); err != nil {
		return err
	}
	if len(observations) > 0 {
		data, err := json.Marshal(observations)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO coordination_runtime_presence(observer_id,peer_id,state,observed_at,expires_at)
 SELECT $1,b.peer_id,i.state,clock_timestamp(),clock_timestamp()+$3*interval '1 second'
 FROM jsonb_to_recordset($2::jsonb) AS i(session_id text,state text)
 JOIN coordination_sessions b ON b.session_id=i.session_id JOIN coordination_peers p ON p.id=b.peer_id
 WHERE p.enabled`, observer, string(data), RuntimePresenceTTL.Seconds())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RemoveRuntimeObserver(ctx context.Context, observer string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM coordination_runtime_presence WHERE observer_id=$1`, observer)
	return err
}

// RuntimePresenceForPeers is a bounded internal read. Callers derive IDs from
// their authenticated workspace inventory, never an arbitrary client argument.
func (s *Store) RuntimePresenceForPeers(ctx context.Context, ids []string) (map[string]RuntimePresence, error) {
	if len(ids) > 10000 {
		return nil, fmt.Errorf("coordination: excessive presence inventory")
	}
	result := make(map[string]RuntimePresence, len(ids))
	for _, id := range ids {
		result[id] = RuntimePresence{State: "unknown", Source: "runtime_lease"}
	}
	if len(ids) == 0 {
		return result, nil
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT peer_id,COUNT(*),MAX(state),MIN(observed_at),MIN(expires_at)
 FROM coordination_runtime_presence WHERE expires_at>clock_timestamp()
 AND peer_id IN (SELECT jsonb_array_elements_text($1::jsonb)) GROUP BY peer_id`, string(data))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var item RuntimePresence
		var observed, expires time.Time
		if err := rows.Scan(&id, &item.Observers, &item.State, &observed, &expires); err != nil {
			return nil, err
		}
		item.Source = "runtime_lease"
		item.ObservedAt = &observed
		item.ExpiresAt = &expires
		if item.Observers > 1 {
			item.State = "multiple_runtimes"
		}
		result[id] = item
	}
	return result, rows.Err()
}
