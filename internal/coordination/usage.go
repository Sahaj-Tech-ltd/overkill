package coordination

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

var ErrUsageBudget = errors.New("coordination: task usage requires review")

type UsageLimits struct {
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"cost_usd"`
}

type TaskUsage struct {
	OwnerPeerID           string      `json:"owner_peer_id,omitempty"`
	PendingExecutions     int64       `json:"pending_executions"`
	UnconfirmedExecutions int64       `json:"unconfirmed_executions"`
	Scope                 string      `json:"scope"`
	TaskID                string      `json:"task_id,omitempty"`
	Limits                UsageLimits `json:"limits"`
	InputTokens           int64       `json:"input_tokens"`
	OutputTokens          int64       `json:"output_tokens"`
	CachedInputTokens     int64       `json:"cached_input_tokens"`
	EstimatedCostUSD      float64     `json:"estimated_cost_usd"`
	UnknownUsage          int64       `json:"unknown_usage"`
	UnknownPrice          int64       `json:"unknown_price"`
	Revision              int64       `json:"revision"`
	RequiresReview        bool        `json:"requires_review"`
	Reason                string      `json:"reason,omitempty"`
}

type UsageSample struct {
	Usage            providers.Usage
	Reported         bool
	EstimatedCostUSD float64
	PriceKnown       bool
}

type usageBudgetKey struct{}

// SendWithBudget is an application control, deliberately absent from model
// SendInput. Limits are committed in the same transaction as the first request.
func (s *Store) SendWithBudget(ctx context.Context, peer string, input SendInput, limits UsageLimits) (Message, error) {
	if err := validateUsageLimits(limits); err != nil {
		return Message{}, err
	}
	return s.Send(context.WithValue(ctx, usageBudgetKey{}, limits), peer, input)
}

func validateUsageLimits(limits UsageLimits) error {
	if limits.Tokens < 0 || limits.Tokens > 1_000_000_000_000 || math.IsNaN(limits.CostUSD) || math.IsInf(limits.CostUSD, 0) || limits.CostUSD < 0 || limits.CostUSD > 1_000_000 {
		return fmt.Errorf("coordination: invalid usage limits")
	}
	return nil
}
func usageScope(m Message) string {
	if m.TaskID != "" {
		return "task:" + m.TaskID
	}
	return "conversation:" + m.ConversationID
}
func inputUsageScope(input SendInput) string {
	return usageScope(Message{TaskID: input.TaskID, ConversationID: input.ConversationID})
}

func ensureUsage(ctx context.Context, tx *sql.Tx, project, scope, task, owner string, limits UsageLimits) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO coordination_task_usage(project_id,scope,task_id,owner_peer,token_limit,cost_limit_nano) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, project, scope, task, owner, limits.Tokens, int64(math.Round(limits.CostUSD*1e9)))
	return err
}

const usageColumns = `owner_peer,scope,task_id,token_limit,cost_limit_nano,input_tokens,output_tokens,cached_tokens,cost_nano,unknown_usage,unknown_price,revision,review_reason`

func scanUsage(row rowScanner) (TaskUsage, error) {
	var u TaskUsage
	var limitNano, costNano int64
	err := row.Scan(&u.OwnerPeerID, &u.Scope, &u.TaskID, &u.Limits.Tokens, &limitNano, &u.InputTokens, &u.OutputTokens, &u.CachedInputTokens, &costNano, &u.UnknownUsage, &u.UnknownPrice, &u.Revision, &u.Reason)
	u.Limits.CostUSD = float64(limitNano) / 1e9
	u.EstimatedCostUSD = float64(costNano) / 1e9
	u.RequiresReview = u.Reason != ""
	return u, err
}
func usageReason(u TaskUsage) string {
	if (u.Limits.Tokens > 0 || u.Limits.CostUSD > 0) && u.UnconfirmedExecutions > 0 {
		return "execution_usage_unknown"
	}
	if u.Limits.Tokens > 0 && u.UnknownUsage > 0 {
		return "usage_unknown"
	}
	if u.Limits.CostUSD > 0 && (u.UnknownUsage > 0 || u.UnknownPrice > 0) {
		return "cost_unknown"
	}
	if u.Limits.Tokens > 0 && (u.InputTokens >= u.Limits.Tokens || u.OutputTokens >= u.Limits.Tokens-u.InputTokens) {
		return "token_limit"
	}
	if u.Limits.CostUSD > 0 && u.EstimatedCostUSD >= u.Limits.CostUSD {
		return "cost_limit"
	}
	return ""
}

// UsageForMessage is participant-filtered, even when a task spans conversations.
func (s *Store) UsageForMessage(ctx context.Context, peer, messageID string) (TaskUsage, error) {
	m, err := s.Read(ctx, peer, messageID)
	if err != nil {
		return TaskUsage{}, err
	}
	u, err := scanUsage(s.db.QueryRowContext(ctx, `SELECT `+usageColumns+` FROM coordination_task_usage WHERE project_id=$1 AND scope=$2`, m.ProjectID, usageScope(m)))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskUsage{Scope: usageScope(m), TaskID: m.TaskID}, nil
	}
	if err == nil {
		err = usageExecutionCounts(ctx, s.db, m.ProjectID, u.Scope, &u)
		reason := usageReason(u)
		if u.UnconfirmedExecutions > 0 && reason == "" {
			reason = "execution_usage_unknown"
		}
		if reason != "" {
			u.RequiresReview = true
			if u.Reason == "" {
				u.Reason = reason
			}
		}
	}
	return u, err
}

// CheckTaskUsage revalidates the live execution and records review evidence once
// per exhausted/unknown budget. Limits gate subsequent calls, not provider bills
// or requests already in flight on another peer.
func (s *Store) CheckTaskUsage(ctx context.Context, messageID string) error {
	return s.updateTaskUsage(ctx, messageID, "", nil)
}
func (s *Store) RecordTaskUsage(ctx context.Context, messageID, key string, sample UsageSample) error {
	if key == "" || len(key) > 128 {
		return fmt.Errorf("coordination: bounded usage key required")
	}
	if sample.Usage.InputTokens < 0 || sample.Usage.InputTokens > 1_000_000_000_000 || sample.Usage.OutputTokens > 1_000_000_000_000 || sample.Usage.OutputTokens < 0 || sample.Usage.CachedInputTokens < 0 || sample.Usage.CachedInputTokens > sample.Usage.InputTokens || math.IsNaN(sample.EstimatedCostUSD) || math.IsInf(sample.EstimatedCostUSD, 0) || sample.EstimatedCostUSD < 0 || sample.EstimatedCostUSD > 1_000_000 {
		return fmt.Errorf("coordination: invalid usage sample")
	}
	return s.updateTaskUsage(ctx, messageID, key, &sample)
}
func (s *Store) updateTaskUsage(ctx context.Context, messageID, key string, sample *UsageSample) error {
	lineage, ok := ctx.Value(executionContextKey{}).(executionLineage)
	if !ok || lineage.messageID != messageID {
		return ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Match global admission's lock order before taking task/message locks.
	if err := lockAdmission(ctx, tx, lineage.lease.PeerID); err != nil {
		return err
	}
	if err := checkLease(ctx, tx, lineage.lease.PeerID, lineage.lease.OwnerID); err != nil {
		return err
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1 AND to_peer=$2 FOR SHARE`, messageID, lineage.lease.PeerID))
	if err != nil {
		return err
	}
	// Reported usage remains attributable after explicit reply completion, but
	// no new call may start once that execution is terminal.
	if sample == nil && m.State != Started {
		return ErrConflict
	}
	if err := ensureUsage(ctx, tx, m.ProjectID, usageScope(m), m.TaskID, m.From, UsageLimits{}); err != nil {
		return err
	}
	u, err := scanUsage(tx.QueryRowContext(ctx, `SELECT `+usageColumns+` FROM coordination_task_usage WHERE project_id=$1 AND scope=$2 FOR UPDATE`, m.ProjectID, usageScope(m)))
	if err != nil {
		return err
	}
	if sample != nil {
		data, _ := json.Marshal(sample)
		result, err := tx.ExecContext(ctx, `INSERT INTO coordination_usage_samples(project_id,scope,message_id,sample_key,sample) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, m.ProjectID, u.Scope, m.ID, key, data)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			var prior []byte
			if err := tx.QueryRowContext(ctx, `SELECT sample FROM coordination_usage_samples WHERE message_id=$1 AND sample_key=$2`, m.ID, key).Scan(&prior); err != nil {
				return err
			}
			var previous UsageSample
			if err := json.Unmarshal(prior, &previous); err != nil {
				return err
			}
			if previous != *sample {
				return ErrIdempotency
			}
		} else {
			u.InputTokens += int64(sample.Usage.InputTokens)
			u.OutputTokens += int64(sample.Usage.OutputTokens)
			u.CachedInputTokens += int64(sample.Usage.CachedInputTokens)
			u.EstimatedCostUSD += math.Ceil(sample.EstimatedCostUSD*1e9) / 1e9
			if !sample.Reported {
				u.UnknownUsage++
			}
			if !sample.PriceKnown {
				u.UnknownPrice++
			}
			u.Revision++
		}
	}
	if err := usageExecutionCounts(ctx, tx, m.ProjectID, u.Scope, &u); err != nil {
		return err
	}
	reason := usageReason(u)
	if reason != "" && u.Reason == "" {
		from, err := enabledPeer(ctx, tx, m.From)
		if err != nil {
			return err
		}
		to, err := enabledPeer(ctx, tx, m.To)
		if err != nil {
			return err
		}
		if err := appendEscalation(ctx, tx, from, to, m.ID, m.ConversationID, "task_"+reason); err != nil {
			return err
		}
		u.Reason = reason
	}
	_, err = tx.ExecContext(ctx, `UPDATE coordination_task_usage SET input_tokens=$3,output_tokens=$4,cached_tokens=$5,cost_nano=$6,unknown_usage=$7,unknown_price=$8,revision=$9,review_reason=$10 WHERE project_id=$1 AND scope=$2`, m.ProjectID, u.Scope, u.InputTokens, u.OutputTokens, u.CachedInputTokens, int64(math.Round(u.EstimatedCostUSD*1e9)), u.UnknownUsage, u.UnknownPrice, u.Revision, u.Reason)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if reason != "" {
		return fmt.Errorf("%w: %s", ErrUsageBudget, reason)
	}
	return nil
}

// A durable execution journal makes a lost worker's missing provider usage
// visible even when its old lease can no longer authorize a late sample.
type usageQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func usageExecutionCounts(ctx context.Context, q usageQuery, project, scope string, u *TaskUsage) error {
	return q.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE NOT p.enabled OR l.peer_id IS NULL OR l.owner_id<>r.owner_id OR l.expires_at<=clock_timestamp()) FROM coordination_usage_runs r JOIN coordination_messages m ON m.id=r.message_id JOIN coordination_peers p ON p.id=m.to_peer LEFT JOIN coordination_leases l ON l.peer_id=m.to_peer WHERE NOT r.finished AND NOT EXISTS(SELECT 1 FROM coordination_usage_run_resolutions x WHERE x.message_id=r.message_id AND x.owner_id=r.owner_id) AND m.project_id=$1 AND CASE WHEN m.task_id<>'' THEN 'task:'||m.task_id ELSE 'conversation:'||m.conversation_id END=$2`, project, scope).Scan(&u.PendingExecutions, &u.UnconfirmedExecutions)
}
func (s *Store) BeginTaskUsage(ctx context.Context, messageID string) error {
	return s.taskUsageRun(ctx, messageID, false)
}
func (s *Store) FinishTaskUsage(ctx context.Context, messageID string) error {
	return s.taskUsageRun(ctx, messageID, true)
}
func (s *Store) taskUsageRun(ctx context.Context, messageID string, finish bool) error {
	lineage, ok := ctx.Value(executionContextKey{}).(executionLineage)
	if !ok || lineage.messageID != messageID {
		return ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockAdmission(ctx, tx, lineage.lease.PeerID); err != nil {
		return err
	}
	if err := checkLease(ctx, tx, lineage.lease.PeerID, lineage.lease.OwnerID); err != nil {
		return err
	}
	var belongs bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_messages WHERE id=$1 AND to_peer=$2)`, messageID, lineage.lease.PeerID).Scan(&belongs); err != nil {
		return err
	}
	if !belongs {
		return ErrUnauthorized
	}
	if finish {
		_, err = tx.ExecContext(ctx, `UPDATE coordination_usage_runs SET finished=true WHERE message_id=$1 AND owner_id=$2`, messageID, lineage.lease.OwnerID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO coordination_usage_runs(message_id,owner_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, messageID, lineage.lease.OwnerID)
		if err == nil {
			var closed bool
			err = tx.QueryRowContext(ctx, `SELECT finished OR EXISTS(SELECT 1 FROM coordination_usage_run_resolutions x WHERE x.message_id=r.message_id AND x.owner_id=r.owner_id) FROM coordination_usage_runs r WHERE message_id=$1 AND owner_id=$2`, messageID, lineage.lease.OwnerID).Scan(&closed)
			if err == nil && closed {
				return fmt.Errorf("%w: execution requires a fresh journal owner", ErrConflict)
			}
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
