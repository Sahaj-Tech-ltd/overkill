package coordination

import (
	"context"
	"fmt"
)

// UsageReconciliationCandidates discovers metadata only. A known root message
// selects the scope; callers cannot supply a project or arbitrary task owner.
func (s *Store) UsageReconciliationCandidates(ctx context.Context, peerID, messageID, afterID string, limit int) (UsageReconciliationCandidatePage, error) {
	page := UsageReconciliationCandidatePage{Candidates: []UsageReconciliationCandidate{}}
	if !canonicalReconciliationID(messageID) || (afterID != "" && !canonicalReconciliationID(afterID)) || limit < 1 || limit > 50 {
		return page, fmt.Errorf("coordination: canonical message/cursor and limit1..50 required")
	}
	ctx, cancel := reconciliationContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	root, u, err := reconciliationTarget(ctx, tx, peerID, messageID)
	if err != nil {
		return page, err
	}
	page.Scope = u.Scope
	rows, err := tx.QueryContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages m WHERE m.project_id=$1 AND CASE WHEN m.task_id<>'' THEN 'task:'||m.task_id ELSE 'conversation:'||m.conversation_id END=$2 AND m.id>$3 AND ($4 OR m.from_peer=$5 OR m.to_peer=$5) AND (EXISTS(SELECT 1 FROM coordination_usage_runs r WHERE r.message_id=m.id AND NOT r.finished AND NOT EXISTS(SELECT 1 FROM coordination_usage_run_resolutions x WHERE x.message_id=r.message_id AND x.owner_id=r.owner_id)) OR EXISTS(SELECT 1 FROM coordination_usage_samples s WHERE s.message_id=m.id AND (NOT COALESCE((s.sample->>'Reported')::boolean,false) OR NOT COALESCE((s.sample->>'PriceKnown')::boolean,false)) AND NOT EXISTS(SELECT 1 FROM coordination_usage_sample_resolutions x WHERE x.message_id=s.message_id AND x.sample_key=s.sample_key))) ORDER BY m.id LIMIT $6`, root.ProjectID, u.Scope, afterID, u.OwnerPeerID == peerID, peerID, limit+1)
	if err != nil {
		return page, err
	}
	var messages []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			rows.Close()
			return page, e
		}
		messages = append(messages, m)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return page, err
	}
	rows.Close()
	if len(messages) > limit {
		page.Truncated = true
		messages = messages[:limit]
	}
	for _, m := range messages {
		candidate := UsageReconciliationCandidate{MessageID: m.ID, MessageRevision: m.Revision, State: m.State}
		err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE NOT p.enabled OR l.peer_id IS NULL OR l.owner_id<>r.owner_id OR l.expires_at<=clock_timestamp()) FROM coordination_usage_runs r JOIN coordination_peers p ON p.id=$2 LEFT JOIN coordination_leases l ON l.peer_id=p.id WHERE r.message_id=$1 AND NOT r.finished AND NOT EXISTS(SELECT 1 FROM coordination_usage_run_resolutions x WHERE x.message_id=r.message_id AND x.owner_id=r.owner_id)`, m.ID, m.To).Scan(&candidate.PendingExecutions, &candidate.UnconfirmedExecutions)
		if err != nil {
			return page, err
		}
		err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE NOT COALESCE((s.sample->>'Reported')::boolean,false)),count(*) FILTER(WHERE NOT COALESCE((s.sample->>'PriceKnown')::boolean,false)) FROM coordination_usage_samples s WHERE s.message_id=$1 AND NOT EXISTS(SELECT 1 FROM coordination_usage_sample_resolutions x WHERE x.message_id=s.message_id AND x.sample_key=s.sample_key)`, m.ID).Scan(&candidate.UnknownUsage, &candidate.UnknownPrice)
		if err != nil {
			return page, err
		}
		candidate.Reason, err = reconciliationEligibility(ctx, tx, peerID, m, u)
		if err != nil {
			return page, err
		}
		candidate.CanReconcile = candidate.Reason == ""
		page.Candidates = append(page.Candidates, candidate)
	}
	if page.Truncated && len(page.Candidates) > 0 {
		page.NextCursor = page.Candidates[len(page.Candidates)-1].MessageID
	}
	return page, nil
}
