package coordination

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// These protocol limits bound autonomous work, not provider token or cost use.
const (
	MaxRequestDepth         = 8
	MaxConversationRequests = 32
	MaxPeerQueue            = 128
	MaxProjectQueue         = 512
	MaxExecutionAttempts    = 3
)

var (
	ErrQueueFull     = errors.New("coordination: queue admission blocked")
	ErrRequestBudget = errors.New("coordination: request budget exhausted")
)

type Escalation struct {
	Sequence        int64     `json:"sequence"`
	ProjectID       string    `json:"project_id"`
	From            string    `json:"from"`
	To              string    `json:"to"`
	ParentMessageID string    `json:"parent_message_id,omitempty"`
	ConversationID  string    `json:"conversation_id,omitempty"`
	Reason          string    `json:"reason"`
	State           State     `json:"state"`
	RequiresReview  bool      `json:"requires_review"`
	CreatedAt       time.Time `json:"created_at"`
}

var budgetDDL = []string{
	`CREATE TABLE IF NOT EXISTS coordination_usage_reconciliations(sequence BIGSERIAL PRIMARY KEY,actor_peer TEXT NOT NULL REFERENCES coordination_peers(id),reconciliation_id TEXT NOT NULL,message_id TEXT NOT NULL REFERENCES coordination_messages(id),request_hash BYTEA NOT NULL,record JSONB NOT NULL,result JSONB NOT NULL,delta_input BIGINT NOT NULL,delta_output BIGINT NOT NULL,delta_cached BIGINT NOT NULL,delta_cost_nano BIGINT NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),UNIQUE(actor_peer,reconciliation_id))`,
	`CREATE INDEX IF NOT EXISTS idx_coordination_reconciliations_message ON coordination_usage_reconciliations(message_id,sequence)`,
	`CREATE TABLE IF NOT EXISTS coordination_request_reviews(actor_peer TEXT NOT NULL REFERENCES coordination_peers(id), review_id TEXT NOT NULL, message_id TEXT NOT NULL REFERENCES coordination_messages(id), request_hash BYTEA NOT NULL, result JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(actor_peer,review_id))`,
	`CREATE TABLE IF NOT EXISTS coordination_usage_runs(message_id TEXT NOT NULL REFERENCES coordination_messages(id), owner_id TEXT NOT NULL, finished BOOLEAN NOT NULL DEFAULT false, PRIMARY KEY(message_id,owner_id))`,
	`CREATE TABLE IF NOT EXISTS coordination_task_usage(project_id TEXT NOT NULL, scope TEXT NOT NULL, task_id TEXT NOT NULL DEFAULT '', owner_peer TEXT NOT NULL REFERENCES coordination_peers(id), token_limit BIGINT NOT NULL DEFAULT 0, cost_limit_nano BIGINT NOT NULL DEFAULT 0, input_tokens BIGINT NOT NULL DEFAULT 0, output_tokens BIGINT NOT NULL DEFAULT 0, cached_tokens BIGINT NOT NULL DEFAULT 0, cost_nano BIGINT NOT NULL DEFAULT 0, unknown_usage BIGINT NOT NULL DEFAULT 0, unknown_price BIGINT NOT NULL DEFAULT 0, revision BIGINT NOT NULL DEFAULT 1, review_reason TEXT NOT NULL DEFAULT '', PRIMARY KEY(project_id,scope))`,
	`CREATE INDEX IF NOT EXISTS idx_coordination_usage_scope ON coordination_messages(project_id,(CASE WHEN task_id<>'' THEN 'task:'||task_id ELSE 'conversation:'||conversation_id END))`,
	`CREATE TABLE IF NOT EXISTS coordination_usage_samples(project_id TEXT NOT NULL, scope TEXT NOT NULL, message_id TEXT NOT NULL REFERENCES coordination_messages(id), sample_key TEXT NOT NULL, sample JSONB NOT NULL, PRIMARY KEY(message_id,sample_key), FOREIGN KEY(project_id,scope) REFERENCES coordination_task_usage(project_id,scope))`,
	`ALTER TABLE coordination_messages ADD COLUMN IF NOT EXISTS parent_message_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE coordination_messages ADD COLUMN IF NOT EXISTS request_depth INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE coordination_messages ADD COLUMN IF NOT EXISTS execution_attempts INTEGER NOT NULL DEFAULT 0`,
	`CREATE INDEX IF NOT EXISTS idx_coordination_conversation ON coordination_messages(project_id,conversation_id,kind)`,
	`CREATE TABLE IF NOT EXISTS coordination_escalation_cursors(project_id TEXT PRIMARY KEY, sequence BIGINT NOT NULL DEFAULT 0)`,
	`CREATE TABLE IF NOT EXISTS coordination_escalations(project_id TEXT NOT NULL, sequence BIGINT NOT NULL, from_peer TEXT NOT NULL REFERENCES coordination_peers(id), to_peer TEXT NOT NULL REFERENCES coordination_peers(id), parent_message_id TEXT NOT NULL DEFAULT '', conversation_id TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(project_id,sequence))`,
	`CREATE TABLE IF NOT EXISTS coordination_usage_run_resolutions(message_id TEXT NOT NULL,owner_id TEXT NOT NULL,reconciliation_sequence BIGINT NOT NULL REFERENCES coordination_usage_reconciliations(sequence),PRIMARY KEY(message_id,owner_id),FOREIGN KEY(message_id,owner_id) REFERENCES coordination_usage_runs(message_id,owner_id))`,
	`CREATE TABLE IF NOT EXISTS coordination_usage_sample_resolutions(message_id TEXT NOT NULL,sample_key TEXT NOT NULL,reconciliation_sequence BIGINT NOT NULL REFERENCES coordination_usage_reconciliations(sequence),PRIMARY KEY(message_id,sample_key),FOREIGN KEY(message_id,sample_key) REFERENCES coordination_usage_samples(message_id,sample_key))`,
}

type executionContextKey struct{}
type executionLineage struct {
	messageID string
	lease     Lease
}

// Only the dispatcher can attach this private execution ancestry. A request's
// model-visible envelope cannot substitute another parent, depth, or lease.
func withExecutionLineage(ctx context.Context, message Message, lease Lease) context.Context {
	return context.WithValue(ctx, executionContextKey{}, executionLineage{messageID: message.ID, lease: lease})
}

func lockAdmission(ctx context.Context, tx *sql.Tx, peerID string) error {
	var project string
	if err := tx.QueryRowContext(ctx, `SELECT project_id FROM coordination_peers WHERE id=$1 AND enabled`, peerID).Scan(&project); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:admission:' || $1,0))`, project)
	return err
}

func appendEscalation(ctx context.Context, tx *sql.Tx, from, to Peer, parent, conversation, reason string) error {
	var sequence int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO coordination_escalation_cursors(project_id,sequence) VALUES($1,1) ON CONFLICT(project_id) DO UPDATE SET sequence=coordination_escalation_cursors.sequence+1 RETURNING sequence`, from.ProjectID).Scan(&sequence); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO coordination_escalations(project_id,sequence,from_peer,to_peer,parent_message_id,conversation_id,reason) VALUES($1,$2,$3,$4,$5,$6,$7)`, from.ProjectID, sequence, from.ID, to.ID, parent, conversation, reason)
	return err
}

func (s *Store) ReadEscalations(ctx context.Context, peerID string, after int64, limit int) ([]Escalation, error) {
	if err := s.validateRepositoryPeerID(ctx, peerID); err != nil {
		return nil, err
	}
	if after < 0 || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("coordination: nonnegative cursor and limit 1..1000 required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.sequence,e.project_id,e.from_peer,e.to_peer,e.parent_message_id,e.conversation_id,e.reason,e.created_at FROM coordination_escalations e JOIN coordination_peers p ON p.id=$1 AND p.enabled AND p.project_id=e.project_id WHERE (e.from_peer=p.id OR e.to_peer=p.id) AND e.sequence>$2 ORDER BY e.sequence LIMIT $3`, peerID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Escalation{}
	for rows.Next() {
		var e Escalation
		if err := rows.Scan(&e.Sequence, &e.ProjectID, &e.From, &e.To, &e.ParentMessageID, &e.ConversationID, &e.Reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.State = Blocked
		e.RequiresReview = true
		result = append(result, e)
	}
	return result, rows.Err()
}

// deriveLineage rechecks the live execution rather than trusting ctx's cached
// Message. Completion/cancellation, peer disable, and lease loss fence sends.
func deriveLineage(ctx context.Context, tx *sql.Tx, from Peer, input *SendInput, replyTo *Message) (string, int, error) {
	parentID, depth := "", 0
	if replyTo != nil {
		parentID, depth = replyTo.ID, replyTo.RequestDepth
	}
	if lineage, ok := ctx.Value(executionContextKey{}).(executionLineage); ok {
		if transport, _ := ctx.Value(transportExecutionKey{}).(bool); transport {
			if err := checkTransportExecution(ctx, tx, from.ID); err != nil {
				return "", 0, err
			}
		}
		if lineage.lease.PeerID != from.ID {
			return "", 0, ErrUnauthorized
		}
		if err := checkLease(ctx, tx, from.ID, lineage.lease.OwnerID); err != nil {
			return "", 0, err
		}
		parent, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1 AND to_peer=$2 AND project_id=$3 FOR SHARE`, lineage.messageID, from.ID, from.ProjectID))
		if err != nil {
			return "", 0, err
		}
		if parent.State != Started {
			return "", 0, ErrConflict
		}
		input.ConversationID, input.TaskID = parent.ConversationID, parent.TaskID
		if input.Deadline.IsZero() || input.Deadline.After(parent.Deadline) {
			input.Deadline = parent.Deadline
		}
		parentID, depth = parent.ID, parent.RequestDepth
	}
	if input.Kind == "request" {
		depth++
	}
	return parentID, depth, nil
}

func checkAdmission(ctx context.Context, tx *sql.Tx, from, to Peer, input SendInput, parent string, depth int, replyTo *Message) error {
	reject := func(reason string, kind error) error {
		if err := appendEscalation(ctx, tx, from, to, parent, input.ConversationID, reason); err != nil {
			return err
		}
		return fmt.Errorf("%w: %s; review coordination escalations", kind, reason)
	}
	// Correlated terminal answers remain deliverable even when request queues
	// are full. Other notifications and uncorrelated answers consume queue slots.
	if replyTo != nil && !replyTo.State.Terminal() && (input.Kind == "answer" || input.Kind == "completion") {
		return nil
	}
	if input.Kind == "request" {
		if depth > MaxRequestDepth {
			return reject("request_depth_limit", ErrRequestBudget)
		}
		if parent != "" {
			current := parent
			seen := map[string]bool{}
			for current != "" {
				if seen[current] || len(seen) >= 64 {
					return reject("request_ancestry_limit", ErrRequestBudget)
				}
				seen[current] = true
				var origin, destination, next string
				if err := tx.QueryRowContext(ctx, `SELECT from_peer,to_peer,parent_message_id FROM coordination_messages WHERE id=$1 AND project_id=$2`, current, from.ProjectID).Scan(&origin, &destination, &next); err != nil {
					return err
				}
				if to.ID == origin || to.ID == destination {
					return reject("request_cycle", ErrRequestBudget)
				}
				current = next
			}
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM coordination_messages WHERE project_id=$1 AND conversation_id=$2 AND kind='request'`, from.ProjectID, input.ConversationID).Scan(&count); err != nil {
			return err
		}
		if count >= MaxConversationRequests {
			return reject("conversation_request_limit", ErrRequestBudget)
		}
	}
	var peerCount, projectCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE to_peer=$2),count(*) FROM coordination_messages WHERE project_id=$1 AND state IN ('accepted','started','blocked')`, from.ProjectID, to.ID).Scan(&peerCount, &projectCount); err != nil {
		return err
	}
	if peerCount >= MaxPeerQueue {
		return reject("peer_queue_limit", ErrQueueFull)
	}
	if projectCount >= MaxProjectQueue {
		return reject("project_queue_limit", ErrQueueFull)
	}
	return nil
}

func budgetRejection(err error) bool {
	return errors.Is(err, ErrQueueFull) || errors.Is(err, ErrRequestBudget)
}

func escalationPayload(reason string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{"reason": reason, "requires_review": true, "limit": MaxExecutionAttempts})
	return data
}

func blockAttemptLimit(ctx context.Context, tx *sql.Tx, recipient Peer, m Message) error {
	sender, err := scanPeer(tx.QueryRowContext(ctx, `SELECT id,project_id,name,role,workspace,capabilities,endpoint,enabled FROM coordination_peers WHERE id=$1`, m.From))
	if err != nil {
		return err
	}
	m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='blocked',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, m.ID))
	if err != nil {
		return err
	}
	if err := appendEvent(ctx, tx, m, "blocked", escalationPayload("execution_attempt_limit")); err != nil {
		return err
	}
	return appendEscalation(ctx, tx, sender, recipient, m.ID, m.ConversationID, "execution_attempt_limit")
}
