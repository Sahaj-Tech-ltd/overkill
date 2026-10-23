package coordination

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ReviewRequestInput is a trusted application control, never a model tool.
// Requeue explicitly acknowledges that replay may repeat earlier side effects.
type ReviewRequestInput struct {
	MessageID             string      `json:"message_id"`
	ExpectedRevision      int64       `json:"expected_revision"`
	ExpectedUsageRevision int64       `json:"expected_usage_revision"`
	ReviewID              string      `json:"review_id"`
	Note                  string      `json:"note"`
	Limits                UsageLimits `json:"limits"`
	Requeue               bool        `json:"requeue"`
}
type ReviewRequestResult struct {
	Message Message   `json:"message"`
	Usage   TaskUsage `json:"usage"`
}

// ReviewRequest changes limits and requeues one blocked request atomically.
// It never starts execution, resets usage, erases uncertainty, or renews deadlines.
// The next worker claim still applies dependency, agreement and attempt gates.
func (s *Store) ReviewRequest(ctx context.Context, peerID string, in ReviewRequestInput) (ReviewRequestResult, error) {
	var empty ReviewRequestResult
	messageID, messageErr := uuid.Parse(in.MessageID)
	id, err := uuid.Parse(in.ReviewID)
	if messageErr != nil || messageID.String() != in.MessageID || err != nil || id.String() != in.ReviewID || !in.Requeue || in.ExpectedRevision < 1 || in.ExpectedUsageRevision < 1 || strings.TrimSpace(in.Note) == "" || len(in.Note) > 2048 || !utf8.ValidString(in.Note) {
		return empty, fmt.Errorf("coordination: review requires canonical UUID, revisions, bounded note and explicit requeue")
	}
	if in.Limits.CostUSD > 0 && math.Round(in.Limits.CostUSD*1e9) == 0 {
		return empty, fmt.Errorf("coordination: positive cost limit must be at least one nanodollar")
	}
	if err := validateUsageLimits(in.Limits); err != nil {
		return empty, err
	}
	body, _ := json.Marshal(in)
	hash := sha256.Sum256(body)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	// Admission precedes peer/message/usage locks, matching Send, Claim and usage
	// accounting. Recipient lock also serializes AcquireLease and transitions.
	if err = lockAdmission(ctx, tx, peerID); err != nil {
		return empty, err
	}
	var toID string
	err = tx.QueryRowContext(ctx, `SELECT to_peer FROM coordination_messages m WHERE id=$1 AND (from_peer=$2 OR to_peer=$2) AND EXISTS(SELECT 1 FROM coordination_peers p WHERE p.id=$2 AND p.enabled AND p.project_id=m.project_id)`, in.MessageID, peerID).Scan(&toID)
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	recipient, err := lockPeer(ctx, tx, toID)
	if err != nil {
		return empty, err
	}
	actor := recipient
	if peerID != toID {
		actor, err = enabledPeer(ctx, tx, peerID)
		if err != nil {
			return empty, err
		}
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1 AND project_id=$2 AND (from_peer=$3 OR to_peer=$3) FOR UPDATE`, in.MessageID, actor.ProjectID, peerID))
	if err != nil {
		return empty, err
	}
	u, err := scanUsage(tx.QueryRowContext(ctx, `SELECT `+usageColumns+` FROM coordination_task_usage WHERE project_id=$1 AND scope=$2 FOR UPDATE`, m.ProjectID, usageScope(m)))
	if err != nil {
		return empty, err
	}
	if u.OwnerPeerID != peerID {
		return empty, ErrUnauthorized
	}
	// Return the original committed receipt before checking now-stale revisions or
	// state. Authority and repository/session identity are nevertheless live.
	var priorHash, priorResult []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM coordination_request_reviews WHERE actor_peer=$1 AND review_id=$2`, peerID, in.ReviewID).Scan(&priorHash, &priorResult)
	if err == nil {
		if !bytes.Equal(priorHash, hash[:]) {
			return empty, ErrIdempotency
		}
		var result ReviewRequestResult
		if err = json.Unmarshal(priorResult, &result); err != nil {
			return empty, err
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	if m.Revision != in.ExpectedRevision || u.Revision != in.ExpectedUsageRevision || m.Kind != "request" || m.State != Blocked {
		return empty, ErrConflict
	}
	var expired, busy bool
	if err = tx.QueryRowContext(ctx, `SELECT $1::timestamptz<=clock_timestamp(),EXISTS(SELECT 1 FROM coordination_leases WHERE peer_id=$2 AND expires_at>clock_timestamp()) OR EXISTS(SELECT 1 FROM coordination_messages WHERE state='started' AND (to_peer=$2 OR (project_id=$3 AND CASE WHEN task_id<>'' THEN 'task:'||task_id ELSE 'conversation:'||conversation_id END=$4)))`, m.Deadline, toID, m.ProjectID, u.Scope).Scan(&expired, &busy); err != nil {
		return empty, err
	}
	if expired {
		return empty, fmt.Errorf("%w: request deadline elapsed", ErrConflict)
	}
	if busy {
		return empty, ErrBusy
	}
	if m.ExecutionAttempts >= MaxExecutionAttempts {
		return empty, ErrRequestBudget
	}
	if err = usageExecutionCounts(ctx, tx, m.ProjectID, u.Scope, &u); err != nil {
		return empty, err
	}
	// Enabled limits cannot be evaded by switching to unlimited in this review.
	// Pending execution evidence is always unresolved, even for unlimited tasks.
	if u.PendingExecutions > 0 || u.UnconfirmedExecutions > 0 || (u.UnknownUsage > 0 && (u.Limits.Tokens > 0 || u.Limits.CostUSD > 0 || in.Limits.Tokens > 0 || in.Limits.CostUSD > 0)) || (u.UnknownPrice > 0 && (u.Limits.CostUSD > 0 || in.Limits.CostUSD > 0)) {
		return empty, fmt.Errorf("%w: unresolved execution or usage evidence", ErrUsageBudget)
	}
	u.Limits = in.Limits
	// Match persisted nanodollar rounding when deciding whether the proposed
	// limit is already exhausted.
	u.Limits.CostUSD = float64(int64(math.Round(in.Limits.CostUSD*1e9))) / 1e9
	if reason := usageReason(u); reason != "" {
		return empty, fmt.Errorf("%w: %s", ErrUsageBudget, reason)
	}
	u.Reason = ""
	u.RequiresReview = false
	u.Revision++
	_, err = tx.ExecContext(ctx, `UPDATE coordination_task_usage SET token_limit=$3,cost_limit_nano=$4,review_reason='',revision=revision+1 WHERE project_id=$1 AND scope=$2`, m.ProjectID, u.Scope, u.Limits.Tokens, int64(math.Round(u.Limits.CostUSD*1e9)))
	if err != nil {
		return empty, err
	}
	m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET state='accepted',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, m.ID))
	if err != nil {
		return empty, err
	}
	evidence, _ := json.Marshal(map[string]any{"review_id": in.ReviewID, "actor_peer_id": peerID, "note": in.Note, "limits": u.Limits, "usage_revision": u.Revision, "requeue": true, "may_repeat_effects": true})
	if err = appendEvent(ctx, tx, m, "reviewed", evidence); err != nil {
		return empty, err
	}
	result := ReviewRequestResult{Message: m, Usage: u}
	receipt, _ := json.Marshal(result)
	_, err = tx.ExecContext(ctx, `INSERT INTO coordination_request_reviews(actor_peer,review_id,message_id,request_hash,result) VALUES($1,$2,$3,$4,$5)`, peerID, in.ReviewID, m.ID, hash[:], receipt)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}
