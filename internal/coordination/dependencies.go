package coordination

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const MaxDependencies = 32

// Graph inspection is metadata only; payloads remain available through Read.
const graphMessageColumns = `id,project_id,from_peer,to_peer,kind,conversation_id,task_id,in_reply_to,'null'::jsonb AS payload,state,revision,created_at,updated_at,deadline,parent_message_id,request_depth,execution_attempts,depends_on,required_contracts`

var dependencyDDL = []string{
	`ALTER TABLE coordination_messages ADD COLUMN IF NOT EXISTS required_contracts JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(required_contracts)='array' AND jsonb_array_length(required_contracts)<=32)`,
	`ALTER TABLE coordination_messages ADD COLUMN IF NOT EXISTS depends_on JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(depends_on)='array' AND jsonb_array_length(depends_on)<=32)`,
	`CREATE INDEX IF NOT EXISTS idx_coordination_dependencies ON coordination_messages USING GIN(depends_on)`,
}

func dependenciesJSON(ids []string) []byte {
	if len(ids) == 0 {
		return []byte(`[]`)
	}
	data, _ := json.Marshal(ids)
	return data
}

func validateDependencies(input SendInput) error {
	if len(input.DependsOn) > MaxDependencies || (len(input.DependsOn) > 0 && input.Kind != "request") {
		return fmt.Errorf("coordination: only requests may have at most %d dependencies", MaxDependencies)
	}
	seen := map[string]bool{}
	for _, id := range input.DependsOn {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id || seen[id] {
			return fmt.Errorf("coordination: dependency IDs must be distinct canonical UUIDs")
		}
		seen[id] = true
	}
	return nil
}

// Existing-only, immutable references make cycles impossible: the new request
// cannot already be an ancestor of any prerequisite. Project admission serializes
// insertion with identity/queue checks; no arbitrary future IDs are accepted.
func authorizeDependencies(ctx context.Context, tx *sql.Tx, from Peer, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM coordination_messages WHERE id=ANY($1) AND project_id=$2 AND kind='request' AND (from_peer=$3 OR to_peer=$3)`, pq.Array(ids), from.ProjectID, from.ID).Scan(&count)
	if err != nil {
		return err
	}
	if count != len(ids) {
		return ErrNotFound
	}
	return nil
}

type DependencyBlocker struct {
	ID     string `json:"id"`
	State  State  `json:"state,omitempty"`
	Reason string `json:"reason"`
}
type DependencyNode struct {
	ContractBlockers []ContractBlocker   `json:"contract_blockers"`
	Message          Message             `json:"message"`
	Readiness        string              `json:"readiness"`
	Blockers         []DependencyBlocker `json:"blockers"`
}
type DependencyGraph struct {
	RootID             string           `json:"root_id"`
	Nodes              []DependencyNode `json:"nodes"`
	Truncated          bool             `json:"truncated"`
	HiddenDependencies bool             `json:"hidden_dependencies"`
}

// Graph returns the root and its prerequisite ancestry from one database
// snapshot. Payload-free envelopes are participant-only. For a prerequisite named by a
// visible envelope, its state is intentionally available as blocker metadata;
// inaccessible envelopes and their ancestry are never traversed or returned.
// Readiness is dependency readiness, not execution admission or provider health.
func (s *Store) Graph(ctx context.Context, viewerPeerID, messageID string, limit int) (DependencyGraph, error) {
	if err := s.validateRepositoryPeerID(ctx, viewerPeerID); err != nil {
		return DependencyGraph{}, err
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 200 {
		return DependencyGraph{}, fmt.Errorf("coordination: graph limit must be 1..200")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return DependencyGraph{}, err
	}
	defer tx.Rollback()
	var project string
	if err = tx.QueryRowContext(ctx, `SELECT project_id FROM coordination_peers WHERE id=$1 AND enabled`, viewerPeerID).Scan(&project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DependencyGraph{}, ErrNotFound
		}
		return DependencyGraph{}, err
	}
	root, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+graphMessageColumns+` FROM coordination_messages WHERE id=$1 AND project_id=$2 AND (from_peer=$3 OR to_peer=$3)`, messageID, project, viewerPeerID))
	if err != nil {
		return DependencyGraph{}, err
	}
	result := DependencyGraph{RootID: messageID, Nodes: []DependencyNode{}}
	queue := []Message{root}
	seen := map[string]bool{root.ID: true}
	for len(queue) > 0 {
		if len(result.Nodes) >= limit {
			result.Truncated = true
			break
		}
		m := queue[0]
		queue = queue[1:]
		node := DependencyNode{Message: m, Readiness: "ready", Blockers: []DependencyBlocker{}}
		if len(m.DependsOn) > 0 {
			rows, err := tx.QueryContext(ctx, `SELECT `+graphMessageColumns+` FROM coordination_messages WHERE id=ANY($1) AND project_id=$2 ORDER BY created_at,id`, pq.Array(m.DependsOn), project)
			if err != nil {
				return DependencyGraph{}, err
			}
			deps := map[string]Message{}
			for rows.Next() {
				d, e := scanMessage(rows)
				if e != nil {
					rows.Close()
					return DependencyGraph{}, e
				}
				deps[d.ID] = d
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return DependencyGraph{}, err
			}
			failed := false
			for _, id := range m.DependsOn {
				d, exists := deps[id]
				reason := "waiting_for_completion"
				if !exists {
					reason = "unavailable_dependency"
					failed = true
				} else if d.State == Failed || d.State == Cancelled {
					reason = "dependency_failed"
					failed = true
				}
				if !exists || d.State != Completed {
					node.Blockers = append(node.Blockers, DependencyBlocker{ID: id, State: d.State, Reason: reason})
				}
				if exists && (d.From == viewerPeerID || d.To == viewerPeerID) {
					if !seen[id] {
						seen[id] = true
						queue = append(queue, d)
					}
				} else {
					result.HiddenDependencies = true
				}
			}
			if failed {
				node.Readiness = "dependency_failed"
			} else if len(node.Blockers) > 0 {
				node.Readiness = "waiting"
			}
		}
		cb, fatal, err := requiredContractBlockers(ctx, tx, project, m.To, m.RequiredContracts)
		if err != nil {
			return DependencyGraph{}, err
		}
		node.ContractBlockers = cb
		if fatal {
			node.Readiness = "dependency_failed"
		} else if len(cb) > 0 && node.Readiness == "ready" {
			node.Readiness = "waiting"
		}
		if m.State.Terminal() {
			node.Readiness = "terminal"
		} else if m.State == Started || m.State == Blocked {
			node.Readiness = "active"
		}
		result.Nodes = append(result.Nodes, node)
	}
	if err = tx.Commit(); err != nil {
		return DependencyGraph{}, err
	}
	return result, nil
}

// failDependencies terminalizes unstarted work whose prerequisites can no longer
// succeed. Each failure and escalation commits with claim, even with no ready
// work. Repeated polling propagates failures across recipients without executing
// tools or automatically retrying a failed task.
func failDependencies(ctx context.Context, tx *sql.Tx, peer Peer) (int, error) {
	changed := 0
	for range MaxPeerQueue {
		m, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages m WHERE to_peer=$1 AND project_id=$2 AND state='accepted' AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(m.depends_on) d(id) LEFT JOIN coordination_messages p ON p.id=d.id AND p.project_id=m.project_id WHERE p.id IS NULL OR p.state IN ('failed','cancelled')) ORDER BY created_at,id LIMIT 1 FOR UPDATE`, peer.ID, peer.ProjectID))
		if err == ErrNotFound {
			return changed, nil
		}
		if err != nil {
			return changed, err
		}
		rows, err := tx.QueryContext(ctx, `SELECT d.id,COALESCE(p.state,'missing') FROM jsonb_array_elements_text($1::jsonb) d(id) LEFT JOIN coordination_messages p ON p.id=d.id AND p.project_id=$2 WHERE p.id IS NULL OR p.state IN ('failed','cancelled') ORDER BY d.id`, dependenciesJSON(m.DependsOn), peer.ProjectID)
		if err != nil {
			return changed, err
		}
		blockers := []DependencyBlocker{}
		for rows.Next() {
			var b DependencyBlocker
			b.Reason = "dependency_failed"
			if err = rows.Scan(&b.ID, &b.State); err != nil {
				rows.Close()
				return changed, err
			}
			blockers = append(blockers, b)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return changed, err
		}
		m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='failed',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, m.ID))
		if err != nil {
			return changed, err
		}
		payload, _ := json.Marshal(struct {
			Reason   string              `json:"reason"`
			Blockers []DependencyBlocker `json:"blockers"`
		}{"dependency_failed", blockers})
		if err = appendEvent(ctx, tx, m, "failed", payload); err != nil {
			return changed, err
		}
		if err = appendEscalation(ctx, tx, Peer{ID: m.From, ProjectID: m.ProjectID}, peer, m.ID, m.ConversationID, "dependency_failed"); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}
