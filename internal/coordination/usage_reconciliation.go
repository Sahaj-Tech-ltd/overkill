package coordination

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

func canonicalReconciliationID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
func reconciliationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 10*time.Second)
}

// This metadata path extends participant access to the current task budget
// owner, without returning any message payload or provider credentials.
func reconciliationTarget(ctx context.Context, tx *sql.Tx, peerID, messageID string) (Message, TaskUsage, error) {
	if err := lockAdmission(ctx, tx, peerID); err != nil {
		return Message{}, TaskUsage{}, err
	}
	actor, err := enabledPeer(ctx, tx, peerID)
	if err != nil {
		return Message{}, TaskUsage{}, err
	}
	m, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages m WHERE id=$1 AND project_id=$2 AND (from_peer=$3 OR to_peer=$3 OR EXISTS(SELECT 1 FROM coordination_task_usage u WHERE u.project_id=m.project_id AND u.scope=CASE WHEN m.task_id<>'' THEN 'task:'||m.task_id ELSE 'conversation:'||m.conversation_id END AND u.owner_peer=$3))`, messageID, actor.ProjectID, peerID))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, TaskUsage{}, ErrNotFound
	}
	if err != nil {
		return Message{}, TaskUsage{}, err
	}
	// Disabled recipients may be reconciled, but remain fenced from execution.
	if _, err = scanPeer(tx.QueryRowContext(ctx, `SELECT id,project_id,name,role,workspace,capabilities,endpoint,enabled FROM coordination_peers WHERE id=$1 FOR UPDATE`, m.To)); err != nil {
		return Message{}, TaskUsage{}, err
	}
	u, err := scanUsage(tx.QueryRowContext(ctx, `SELECT `+usageColumns+` FROM coordination_task_usage WHERE project_id=$1 AND scope=$2 FOR UPDATE`, m.ProjectID, usageScope(m)))
	if err != nil {
		return Message{}, TaskUsage{}, err
	}
	m, err = scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM coordination_messages WHERE id=$1 FOR UPDATE`, messageID))
	if err != nil {
		return Message{}, TaskUsage{}, err
	}
	// AcquireLease is fenced by the recipient row lock. A live lease rejects
	// reconciliation; an expired lease cannot be renewed. Do not hold a lease
	// share lock here: metadata reads must not stall a live worker's heartbeat.

	return m, u, nil
}
func reconciliationEligibility(ctx context.Context, tx *sql.Tx, peerID string, m Message, u TaskUsage) (string, error) {
	if u.OwnerPeerID != peerID {
		return "not_budget_owner", nil
	}
	if m.State == Started {
		return "execution_still_started", nil
	}
	var live, started bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_leases WHERE peer_id=$1 AND expires_at>clock_timestamp()),EXISTS(SELECT 1 FROM coordination_messages WHERE to_peer=$1 AND state='started')`, m.To).Scan(&live, &started); err != nil {
		return "", err
	}
	if live {
		return "recipient_live_lease", nil
	}
	if started {
		return "recipient_started_work", nil
	}
	var pending int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM coordination_usage_runs r WHERE r.message_id=$1 AND NOT r.finished AND NOT EXISTS(SELECT 1 FROM coordination_usage_run_resolutions x WHERE x.message_id=r.message_id AND x.owner_id=r.owner_id)`, m.ID).Scan(&pending); err != nil {
		return "", err
	}
	if pending == 0 {
		var uncertain bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coordination_usage_samples s WHERE s.message_id=$1 AND (NOT COALESCE((s.sample->>'Reported')::boolean,false) OR NOT COALESCE((s.sample->>'PriceKnown')::boolean,false)) AND NOT EXISTS(SELECT 1 FROM coordination_usage_sample_resolutions x WHERE x.message_id=s.message_id AND x.sample_key=s.sample_key))`, m.ID).Scan(&uncertain); err != nil {
			return "", err
		}
		if !uncertain {
			return "no_uncertain_usage", nil
		}
	}
	return "", nil
}
func hashReconciliation(h hash.Hash, value any) {
	encoded, _ := json.Marshal(value)
	h.Write(encoded)
	h.Write([]byte{'\n'})
}
func checkedUsageAdd(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b || b < 0 && a < math.MinInt64-b {
		return 0, fmt.Errorf("coordination: usage overflow")
	}
	return a + b, nil
}
func costToNano(value float64) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1_000_000 {
		return 0, fmt.Errorf("coordination: bounded nonnegative cost required")
	}
	nano := int64(math.Ceil(value * 1e9))
	return nano, nil
}
func addReconciliationTotals(t *UsageReconciliationTotals, input, output, cached, cost int64) error {
	var err error
	if t.InputTokens, err = checkedUsageAdd(t.InputTokens, input); err != nil {
		return err
	}
	if t.OutputTokens, err = checkedUsageAdd(t.OutputTokens, output); err != nil {
		return err
	}
	if t.CachedInputTokens, err = checkedUsageAdd(t.CachedInputTokens, cached); err != nil {
		return err
	}
	nano := int64(math.Round(t.EstimatedCostUSD * 1e9))
	nano, err = checkedUsageAdd(nano, cost)
	if err != nil {
		return err
	}
	t.EstimatedCostUSD = float64(nano) / 1e9
	return nil
}
func reconciliationSnapshot(ctx context.Context, tx *sql.Tx, peerID string, m Message, u TaskUsage) (UsageReconciliationSnapshot, error) {
	out := UsageReconciliationSnapshot{MessageID: m.ID, MessageRevision: m.Revision, UsageRevision: u.Revision, Scope: u.Scope, OwnerPeerID: u.OwnerPeerID, Runs: []UsageJournalRun{}, Samples: []UsageSampleEvidence{}, Previous: []UsageReconciliationRecord{}}
	h := sha256.New()
	hashReconciliation(h, []string{m.ID, m.ProjectID, u.Scope})
	rows, err := tx.QueryContext(ctx, `SELECT r.owner_id,r.finished,EXISTS(SELECT 1 FROM coordination_usage_run_resolutions x WHERE x.message_id=r.message_id AND x.owner_id=r.owner_id) FROM coordination_usage_runs r WHERE r.message_id=$1 ORDER BY r.owner_id`, m.ID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var run UsageJournalRun
		if err = rows.Scan(&run.OwnerID, &run.Finished, &run.Resolved); err != nil {
			rows.Close()
			return out, err
		}
		out.RunCount++
		hashReconciliation(h, run)
		if len(out.Runs) < 128 {
			out.Runs = append(out.Runs, run)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	out.RunsTruncated = out.RunCount > int64(len(out.Runs))
	rows, err = tx.QueryContext(ctx, `SELECT s.sample_key,s.sample,EXISTS(SELECT 1 FROM coordination_usage_sample_resolutions x WHERE x.message_id=s.message_id AND x.sample_key=s.sample_key) FROM coordination_usage_samples s WHERE s.message_id=$1 ORDER BY s.sample_key`, m.ID)
	if err != nil {
		return out, err
	}
	var unknownCostNano, observedCostNano int64
	for rows.Next() {
		var key string
		var raw []byte
		var resolved bool
		if err = rows.Scan(&key, &raw, &resolved); err != nil {
			rows.Close()
			return out, err
		}
		var sample UsageSample
		if err = json.Unmarshal(raw, &sample); err != nil {
			rows.Close()
			return out, err
		}
		out.SampleCount++
		hashReconciliation(h, []any{key, json.RawMessage(raw), resolved})
		cost, e := costToNano(sample.EstimatedCostUSD)
		if e != nil {
			rows.Close()
			return out, e
		}
		observedCostNano, e = checkedUsageAdd(observedCostNano, cost)
		if e != nil {
			rows.Close()
			return out, e
		}
		if e = addReconciliationTotals(&out.Observed, int64(sample.Usage.InputTokens), int64(sample.Usage.OutputTokens), int64(sample.Usage.CachedInputTokens), cost); e != nil {
			rows.Close()
			return out, e
		}
		if !sample.Reported {
			out.Observed.UnknownUsage++
			if !resolved {
				out.Contribution.UnknownUsage++
			}
		}
		if !sample.PriceKnown {
			out.Observed.UnknownPrice++
			if !resolved {
				out.Contribution.UnknownPrice++
				unknownCostNano, e = checkedUsageAdd(unknownCostNano, cost)
				if e != nil {
					rows.Close()
					return out, e
				}
			}
		}
		if len(out.Samples) < 128 {
			out.Samples = append(out.Samples, UsageSampleEvidence{Key: key, InputTokens: int64(sample.Usage.InputTokens), OutputTokens: int64(sample.Usage.OutputTokens), CachedInputTokens: int64(sample.Usage.CachedInputTokens), EstimatedCostUSD: sample.EstimatedCostUSD, UsageKnown: sample.Reported, PriceKnown: sample.PriceKnown})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	out.SamplesTruncated = out.SampleCount > int64(len(out.Samples))
	out.Observed.EstimatedCostUSD = float64(observedCostNano) / 1e9
	out.contributionCostNano = observedCostNano
	if err = addReconciliationTotals(&out.Contribution, out.Observed.InputTokens, out.Observed.OutputTokens, out.Observed.CachedInputTokens, 0); err != nil {
		return out, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT sequence,request_hash,record,delta_input,delta_output,delta_cached,delta_cost_nano FROM coordination_usage_reconciliations WHERE message_id=$1 ORDER BY sequence`, m.ID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var sequence, di, do, dc, dn int64
		var bodyHash, recordRaw []byte
		if err = rows.Scan(&sequence, &bodyHash, &recordRaw, &di, &do, &dc, &dn); err != nil {
			rows.Close()
			return out, err
		}
		hashReconciliation(h, []any{sequence, bodyHash, json.RawMessage(recordRaw), di, do, dc, dn})
		if err = addReconciliationTotals(&out.Contribution, di, do, dc, 0); err != nil {
			rows.Close()
			return out, err
		}
		out.contributionCostNano, err = checkedUsageAdd(out.contributionCostNano, dn)
		if err != nil {
			rows.Close()
			return out, err
		}
		var record UsageReconciliationRecord
		if err = json.Unmarshal(recordRaw, &record); err != nil {
			rows.Close()
			return out, err
		}
		out.PreviousCount++
		if len(out.Previous) == 16 {
			out.Previous = out.Previous[1:]
		}
		out.Previous = append(out.Previous, record)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	out.PreviousTruncated = out.PreviousCount > int64(len(out.Previous))
	out.Contribution.EstimatedCostUSD = float64(out.contributionCostNano) / 1e9
	knownNano := out.contributionCostNano - unknownCostNano
	out.knownCostNano = knownNano
	if knownNano < 0 || out.Contribution.InputTokens < 0 || out.Contribution.OutputTokens < 0 || out.Contribution.CachedInputTokens < 0 || out.Contribution.CachedInputTokens > out.Contribution.InputTokens {
		return out, fmt.Errorf("coordination: inconsistent reconciled contribution")
	}
	out.KnownCostFloorUSD = float64(knownNano) / 1e9
	out.JournalFingerprint = hex.EncodeToString(h.Sum(nil))
	out.Reason, err = reconciliationEligibility(ctx, tx, peerID, m, u)
	out.CanReconcile = out.Reason == ""
	return out, err
}
func (s *Store) UsageReconciliationForMessage(ctx context.Context, peerID, messageID string) (UsageReconciliationSnapshot, error) {
	if !canonicalReconciliationID(messageID) {
		return UsageReconciliationSnapshot{}, ErrNotFound
	}
	ctx, cancel := reconciliationContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UsageReconciliationSnapshot{}, err
	}
	defer tx.Rollback()
	m, u, err := reconciliationTarget(ctx, tx, peerID, messageID)
	if err != nil {
		return UsageReconciliationSnapshot{}, err
	}
	out, err := reconciliationSnapshot(ctx, tx, peerID, m, u)
	out.contributionCostNano = 0
	out.knownCostNano = 0
	return out, err
}
func validateReconciliationInput(in ReconcileTaskUsageInput) error {
	fingerprint, err := hex.DecodeString(in.ExpectedJournalFingerprint)
	if !canonicalReconciliationID(in.MessageID) || !canonicalReconciliationID(in.ReconciliationID) || in.ExpectedMessageRevision < 1 || in.ExpectedUsageRevision < 1 || err != nil || len(fingerprint) != 32 || hex.EncodeToString(fingerprint) != in.ExpectedJournalFingerprint || !in.UsageKnown || !in.PriceKnown || !in.AcknowledgeUncertainty {
		return fmt.Errorf("coordination: reconciliation requires canonical IDs, revisions, evidence fingerprint and explicit manual attestation")
	}
	if in.InputTokens < 0 || in.InputTokens > 1_000_000_000_000 || in.OutputTokens < 0 || in.OutputTokens > 1_000_000_000_000 || in.CachedInputTokens < 0 || in.CachedInputTokens > in.InputTokens {
		return fmt.Errorf("coordination: invalid replacement usage")
	}
	if _, err = costToNano(in.CostUSD); err != nil {
		return err
	}
	if in.Basis != "external_record" && in.Basis != "conservative_estimate" {
		return fmt.Errorf("coordination: explicit reconciliation basis required")
	}
	for _, item := range []struct {
		value string
		max   int
	}{{in.Reason, 2048}, {in.Evidence, 8192}} {
		if strings.TrimSpace(item.value) == "" || len(item.value) > item.max || !utf8.ValidString(item.value) || strings.ContainsRune(item.value, 0) {
			return fmt.Errorf("coordination: bounded reason and evidence required")
		}
	}
	return nil
}

// ReconcileTaskUsage is a trusted application control. Its values are manual
// attestations, never provider reports or independently verified invoices.
func (s *Store) ReconcileTaskUsage(ctx context.Context, peerID string, in ReconcileTaskUsageInput) (ReconcileTaskUsageResult, error) {
	var empty ReconcileTaskUsageResult
	if _, model := ctx.Value(executionContextKey{}).(executionLineage); model {
		return empty, ErrUnauthorized
	}
	if err := validateReconciliationInput(in); err != nil {
		return empty, err
	}
	ctx, cancel := reconciliationContext(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	m, u, err := reconciliationTarget(ctx, tx, peerID, in.MessageID)
	if err != nil {
		return empty, err
	}
	if u.OwnerPeerID != peerID {
		return empty, ErrUnauthorized
	}
	body, _ := json.Marshal(in)
	requestHash := sha256.Sum256(body)
	var oldHash, oldResult []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM coordination_usage_reconciliations WHERE actor_peer=$1 AND reconciliation_id=$2`, peerID, in.ReconciliationID).Scan(&oldHash, &oldResult)
	if err == nil {
		if !bytes.Equal(oldHash, requestHash[:]) {
			return empty, ErrIdempotency
		}
		var result ReconcileTaskUsageResult
		if err = json.Unmarshal(oldResult, &result); err != nil {
			return empty, err
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	// Mutation must serialize with an in-flight renewal. A renewal may have
	// begun before the old expiry and hold an uncommitted future expiry; an
	// ordinary SELECT would still see the expired committed version. Locking
	// here waits for that renewal, then eligibility observes its committed row.
	var leaseOwner string
	err = tx.QueryRowContext(ctx, `SELECT owner_id FROM coordination_leases WHERE peer_id=$1 FOR UPDATE`, m.To).Scan(&leaseOwner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	snapshot, err := reconciliationSnapshot(ctx, tx, peerID, m, u)
	if err != nil {
		return empty, err
	}
	if m.Revision != in.ExpectedMessageRevision || u.Revision != in.ExpectedUsageRevision || snapshot.JournalFingerprint != in.ExpectedJournalFingerprint {
		return empty, ErrConflict
	}
	if !snapshot.CanReconcile {
		if snapshot.Reason == "recipient_live_lease" || snapshot.Reason == "execution_still_started" || snapshot.Reason == "recipient_started_work" {
			return empty, ErrBusy
		}
		return empty, ErrConflict
	}
	replacementNano, _ := costToNano(in.CostUSD)
	requiredCostFloor := snapshot.knownCostNano
	if in.Basis == "conservative_estimate" && snapshot.contributionCostNano > requiredCostFloor {
		requiredCostFloor = snapshot.contributionCostNano
	}
	if in.InputTokens < snapshot.Contribution.InputTokens || in.OutputTokens < snapshot.Contribution.OutputTokens || in.CachedInputTokens < snapshot.Contribution.CachedInputTokens || replacementNano < requiredCostFloor {
		return empty, fmt.Errorf("coordination: replacement cannot reduce recorded or previously attested known usage")
	}
	// Fencing an expired owner is explicit and serialized with peer acquisition.
	if _, err = tx.ExecContext(ctx, `DELETE FROM coordination_leases WHERE peer_id=$1 AND expires_at<=clock_timestamp()`, m.To); err != nil {
		return empty, err
	}
	after := UsageReconciliationTotals{InputTokens: in.InputTokens, OutputTokens: in.OutputTokens, CachedInputTokens: in.CachedInputTokens, EstimatedCostUSD: float64(replacementNano) / 1e9}
	di, do, dc := after.InputTokens-snapshot.Contribution.InputTokens, after.OutputTokens-snapshot.Contribution.OutputTokens, after.CachedInputTokens-snapshot.Contribution.CachedInputTokens
	dn := replacementNano - snapshot.contributionCostNano
	if u.InputTokens, err = checkedUsageAdd(u.InputTokens, di); err != nil {
		return empty, err
	}
	if u.OutputTokens, err = checkedUsageAdd(u.OutputTokens, do); err != nil {
		return empty, err
	}
	if u.CachedInputTokens, err = checkedUsageAdd(u.CachedInputTokens, dc); err != nil {
		return empty, err
	}
	var aggregateCostNano int64
	if err = tx.QueryRowContext(ctx, `SELECT cost_nano FROM coordination_task_usage WHERE project_id=$1 AND scope=$2`, m.ProjectID, u.Scope).Scan(&aggregateCostNano); err != nil {
		return empty, err
	}
	costNano, err := checkedUsageAdd(aggregateCostNano, dn)
	if err != nil || costNano < 0 {
		return empty, fmt.Errorf("coordination: invalid reconciled cost total")
	}
	u.EstimatedCostUSD = float64(costNano) / 1e9
	u.UnknownUsage -= snapshot.Contribution.UnknownUsage
	u.UnknownPrice -= snapshot.Contribution.UnknownPrice
	if u.UnknownUsage < 0 || u.UnknownPrice < 0 {
		return empty, fmt.Errorf("coordination: inconsistent unknown usage counters")
	}
	if u.Revision < 1 || u.Revision == math.MaxInt64 {
		return empty, fmt.Errorf("coordination: usage revision exhausted")
	}
	u.Revision++
	record := UsageReconciliationRecord{ReconciliationID: in.ReconciliationID, MessageID: m.ID, ActorPeerID: peerID, Basis: in.Basis, Reason: in.Reason, Evidence: in.Evidence, Provenance: "manual_attestation", Before: snapshot.Contribution, After: after, CreatedAt: time.Now().UTC()}
	recordRaw, _ := json.Marshal(record)
	var sequence int64
	err = tx.QueryRowContext(ctx, `INSERT INTO coordination_usage_reconciliations(actor_peer,reconciliation_id,message_id,request_hash,record,result,delta_input,delta_output,delta_cached,delta_cost_nano) VALUES($1,$2,$3,$4,$5,'{}',$6,$7,$8,$9) RETURNING sequence`, peerID, in.ReconciliationID, m.ID, requestHash[:], recordRaw, di, do, dc, dn).Scan(&sequence)
	if err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO coordination_usage_run_resolutions(message_id,owner_id,reconciliation_sequence) SELECT message_id,owner_id,$2 FROM coordination_usage_runs WHERE message_id=$1 AND NOT finished ON CONFLICT DO NOTHING`, m.ID, sequence); err != nil {
		return empty, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO coordination_usage_sample_resolutions(message_id,sample_key,reconciliation_sequence) SELECT message_id,sample_key,$2 FROM coordination_usage_samples WHERE message_id=$1 ON CONFLICT DO NOTHING`, m.ID, sequence); err != nil {
		return empty, err
	}
	if err = usageExecutionCounts(ctx, tx, m.ProjectID, u.Scope, &u); err != nil {
		return empty, err
	}
	u.Reason = usageReason(u)
	if u.UnconfirmedExecutions > 0 && u.Reason == "" {
		u.Reason = "execution_usage_unknown"
	}
	u.RequiresReview = u.Reason != ""
	_, err = tx.ExecContext(ctx, `UPDATE coordination_task_usage SET input_tokens=$3,output_tokens=$4,cached_tokens=$5,cost_nano=$6,unknown_usage=$7,unknown_price=$8,revision=$9,review_reason=$10 WHERE project_id=$1 AND scope=$2`, m.ProjectID, u.Scope, u.InputTokens, u.OutputTokens, u.CachedInputTokens, costNano, u.UnknownUsage, u.UnknownPrice, u.Revision, u.Reason)
	if err != nil {
		return empty, err
	}
	m, err = scanMessage(tx.QueryRowContext(ctx, `UPDATE coordination_messages SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING `+messageColumns, m.ID))
	if err != nil {
		return empty, err
	}
	audit, _ := json.Marshal(record)
	if err = appendEvent(ctx, tx, m, "usage_reconciled", audit); err != nil {
		return empty, err
	}
	refreshed, err := reconciliationSnapshot(ctx, tx, peerID, m, u)
	if err != nil {
		return empty, err
	}
	refreshed.contributionCostNano = 0
	refreshed.knownCostNano = 0
	result := ReconcileTaskUsageResult{Usage: u, Reconciliation: record, Snapshot: refreshed}
	encoded, _ := json.Marshal(result)
	if _, err = tx.ExecContext(ctx, `UPDATE coordination_usage_reconciliations SET result=$2 WHERE sequence=$1`, sequence, encoded); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}
