package coordination

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func stoppedUsage(t *testing.T, s *Store, from, to Peer, task string, limits UsageLimits, samples ...UsageSample) (Message, context.Context, Lease) {
	t.Helper()
	in := request(uuid.NewString())
	in.To = to.Name
	in.TaskID = task
	m, err := s.SendWithBudget(t.Context(), from.ID, in, limits)
	require.NoError(t, err)
	ctx, _, lease := claimLineage(t, s, to)
	require.NoError(t, s.BeginTaskUsage(ctx, m.ID))
	for i, sample := range samples {
		err = s.RecordTaskUsage(ctx, m.ID, fmt.Sprint(i), sample)
		if err != nil {
			require.ErrorIs(t, err, ErrUsageBudget)
		}
	}
	current, err := s.Read(t.Context(), to.ID, m.ID)
	require.NoError(t, err)
	m, err = s.WorkerTransition(t.Context(), lease, m.ID, current.Revision, "blocked", json.RawMessage(`{"reason":"interrupted fixture"}`))
	require.NoError(t, err)
	require.NoError(t, s.ReleaseLease(t.Context(), lease))
	return m, ctx, lease
}
func reconciliationInput(t *testing.T, s *Store, peer string, m Message) ReconcileTaskUsageInput {
	t.Helper()
	snapshot, err := s.UsageReconciliationForMessage(t.Context(), peer, m.ID)
	require.NoError(t, err)
	return ReconcileTaskUsageInput{MessageID: m.ID, ExpectedMessageRevision: snapshot.MessageRevision, ExpectedUsageRevision: snapshot.UsageRevision, ExpectedJournalFingerprint: snapshot.JournalFingerprint, ReconciliationID: uuid.NewString(), InputTokens: snapshot.Contribution.InputTokens, OutputTokens: snapshot.Contribution.OutputTokens, CachedInputTokens: snapshot.Contribution.CachedInputTokens, CostUSD: snapshot.Contribution.EstimatedCostUSD, UsageKnown: true, PriceKnown: true, Reason: "Reviewed stopped execution usage", Evidence: "Operator supplied external run record; not independently verified", Basis: "external_record", AcknowledgeUncertainty: true}
}
func TestUsageReconciliationPreservesJournalAndReceiptAndDoesNotReplay(t *testing.T) {
	s, front, back, _ := fixture(t)
	m, _, oldLease := stoppedUsage(t, s, front, back, "reconcile", UsageLimits{Tokens: 25, CostUSD: 1}, UsageSample{Usage: providers.Usage{InputTokens: 10, OutputTokens: 2, CachedInputTokens: 3}, Reported: true, PriceKnown: true, EstimatedCostUSD: .1}, UsageSample{Usage: providers.Usage{InputTokens: 7, OutputTokens: 1}, Reported: false, PriceKnown: false, EstimatedCostUSD: .3})
	snapshot, err := s.UsageReconciliationForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.True(t, snapshot.CanReconcile)
	require.EqualValues(t, 1, snapshot.RunCount)
	require.InDelta(t, .1, snapshot.KnownCostFloorUSD, 1e-9)
	in := reconciliationInput(t, s, front.ID, m)
	in.InputTokens = 30
	in.OutputTokens = 4
	in.CostUSD = .2
	conservative := in
	conservative.Basis = "conservative_estimate"
	_, err = s.ReconcileTaskUsage(t.Context(), front.ID, conservative)
	require.Error(t, err, "conservative estimates cannot lower the currently recorded estimate")
	got, err := s.ReconcileTaskUsage(t.Context(), front.ID, in)
	require.NoError(t, err)
	require.EqualValues(t, 30, got.Usage.InputTokens)
	require.EqualValues(t, 4, got.Usage.OutputTokens)
	require.Zero(t, got.Usage.UnknownUsage)
	require.Zero(t, got.Usage.UnknownPrice)
	require.Zero(t, got.Usage.PendingExecutions)
	require.Equal(t, "token_limit", got.Usage.Reason)
	require.Equal(t, "manual_attestation", got.Reconciliation.Provenance)
	persisted, err := s.Read(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, Blocked, persisted.State)
	require.Equal(t, m.Revision+1, persisted.Revision)
	var finished bool
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT finished FROM coordination_usage_runs WHERE message_id=$1 AND owner_id=$2`, m.ID, oldLease.OwnerID).Scan(&finished))
	require.False(t, finished, "original unknown journal is immutable")
	var sampleCount int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_usage_samples WHERE message_id=$1`, m.ID).Scan(&sampleCount))
	require.Equal(t, 2, sampleCount)
	reopened, err := NewStore(t.Context(), s.db)
	require.NoError(t, err)
	repeat, err := reopened.ReconcileTaskUsage(t.Context(), front.ID, in)
	require.NoError(t, err)
	require.Equal(t, got, repeat)
	changed := in
	changed.Evidence = "different attestation"
	_, err = s.ReconcileTaskUsage(t.Context(), front.ID, changed)
	require.ErrorIs(t, err, ErrIdempotency)
	usage, err := s.UsageForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.Zero(t, usage.UnconfirmedExecutions)
	require.True(t, usage.RequiresReview)
	require.Equal(t, "token_limit", usage.Reason)
}
func TestUsageReconciliationAuthorizationLeaseAndLowerBounds(t *testing.T) {
	s, front, back, _ := fixture(t)
	m, ctx, _ := stoppedUsage(t, s, front, back, "", UsageLimits{}, UsageSample{Usage: providers.Usage{InputTokens: 10, CachedInputTokens: 4}, Reported: true, PriceKnown: true, EstimatedCostUSD: .1})
	in := reconciliationInput(t, s, front.ID, m)
	_, err := s.ReconcileTaskUsage(t.Context(), back.ID, in)
	require.ErrorIs(t, err, ErrUnauthorized)
	_, err = s.ReconcileTaskUsage(ctx, front.ID, in)
	require.ErrorIs(t, err, ErrUnauthorized)
	outsider, _, err := s.RegisterPeer(t.Context(), Peer{ProjectID: "unrelated", Name: "outsider", Role: "worker", Workspace: t.TempDir()})
	require.NoError(t, err)
	_, err = s.UsageReconciliationForMessage(t.Context(), outsider.ID, m.ID)
	require.ErrorIs(t, err, ErrNotFound)
	for _, bad := range []ReconcileTaskUsageInput{func() ReconcileTaskUsageInput { x := in; x.InputTokens = 9; return x }(), func() ReconcileTaskUsageInput { x := in; x.CachedInputTokens = 3; return x }(), func() ReconcileTaskUsageInput { x := in; x.CostUSD = .01; return x }(), func() ReconcileTaskUsageInput { x := in; x.AcknowledgeUncertainty = false; return x }()} {
		_, err = s.ReconcileTaskUsage(t.Context(), front.ID, bad)
		require.Error(t, err)
	}
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO coordination_leases(peer_id,owner_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '30 seconds')`, back.ID, uuid.NewString())
	require.NoError(t, err)
	_, err = s.ReconcileTaskUsage(t.Context(), front.ID, in)
	require.ErrorIs(t, err, ErrBusy)
	_, err = s.db.ExecContext(t.Context(), `UPDATE coordination_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE peer_id=$1`, back.ID)
	require.NoError(t, err)
	_, err = s.ReconcileTaskUsage(t.Context(), front.ID, in)
	require.NoError(t, err)
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_leases WHERE peer_id=$1`, back.ID).Scan(&count))
	require.Zero(t, count)
}
func TestUsageReconciliationWholeEvidenceFingerprintAndConcurrentReceipt(t *testing.T) {
	s, front, back, _ := fixture(t)
	m, _, _ := stoppedUsage(t, s, front, back, "large", UsageLimits{})
	// A historical import has more rows than the preview cap. Fingerprinting and
	// aggregate floors must include the undisplayed evidence too.
	for i := 0; i < 130; i++ {
		raw, _ := json.Marshal(UsageSample{Usage: providers.Usage{InputTokens: 1}, Reported: true, PriceKnown: true})
		_, err := s.db.ExecContext(t.Context(), `INSERT INTO coordination_usage_samples(project_id,scope,message_id,sample_key,sample) VALUES($1,$2,$3,$4,$5)`, m.ProjectID, usageScope(m), m.ID, fmt.Sprintf("%03d", i), raw)
		require.NoError(t, err)
	}
	_, err := s.db.ExecContext(t.Context(), `UPDATE coordination_task_usage SET input_tokens=130 WHERE project_id=$1 AND scope=$2`, m.ProjectID, usageScope(m))
	require.NoError(t, err)
	snap, err := s.UsageReconciliationForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.EqualValues(t, 130, snap.SampleCount)
	require.Len(t, snap.Samples, 128)
	require.True(t, snap.SamplesTruncated)
	require.EqualValues(t, 130, snap.Contribution.InputTokens)
	stale := reconciliationInput(t, s, front.ID, m)
	raw, _ := json.Marshal(UsageSample{Usage: providers.Usage{InputTokens: 2}, Reported: true, PriceKnown: true})
	_, err = s.db.ExecContext(t.Context(), `UPDATE coordination_usage_samples SET sample=$2 WHERE message_id=$1 AND sample_key='129'`, m.ID, raw)
	require.NoError(t, err)
	_, err = s.ReconcileTaskUsage(t.Context(), front.ID, stale)
	require.ErrorIs(t, err, ErrConflict)
	// Restore the imported total before exercising atomic duplicate receipt writes.
	_, err = s.db.ExecContext(t.Context(), `UPDATE coordination_task_usage SET input_tokens=131 WHERE project_id=$1 AND scope=$2`, m.ProjectID, usageScope(m))
	require.NoError(t, err)
	in := reconciliationInput(t, s, front.ID, m)
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() { _, err := s.ReconcileTaskUsage(t.Context(), front.ID, in); failures <- err })
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_usage_reconciliations WHERE message_id=$1`, m.ID).Scan(&count))
	require.Equal(t, 1, count)
}
func TestUsageReconciliationOtherMessageUncertaintyAndCandidateDiscovery(t *testing.T) {
	s, front, back, _ := fixture(t)
	third, _, err := s.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: "third", Role: "worker", Workspace: t.TempDir()})
	require.NoError(t, err)
	root, _, _ := stoppedUsage(t, s, front, back, "shared", UsageLimits{Tokens: 100}, UsageSample{Usage: providers.Usage{InputTokens: 3}, Reported: true, PriceKnown: true})
	other, _, _ := stoppedUsage(t, s, front, third, "shared", UsageLimits{Tokens: 100}, UsageSample{Usage: providers.Usage{InputTokens: 5}, Reported: false, PriceKnown: false})
	page, err := s.UsageReconciliationCandidates(t.Context(), front.ID, root.ID, "", 1)
	require.NoError(t, err)
	require.True(t, page.Truncated)
	require.NotEmpty(t, page.NextCursor)
	require.Len(t, page.Candidates, 1)
	second, err := s.UsageReconciliationCandidates(t.Context(), front.ID, root.ID, page.NextCursor, 1)
	require.NoError(t, err)
	require.Len(t, second.Candidates, 1)
	require.False(t, second.Truncated)
	require.NotEqual(t, page.Candidates[0].MessageID, second.Candidates[0].MessageID)
	_, err = s.ReconcileTaskUsage(t.Context(), front.ID, reconciliationInput(t, s, front.ID, root))
	require.NoError(t, err)
	usage, err := s.UsageForMessage(t.Context(), front.ID, root.ID)
	require.NoError(t, err)
	require.EqualValues(t, 8, usage.InputTokens)
	require.EqualValues(t, 1, usage.UnknownUsage)
	require.EqualValues(t, 1, usage.UnconfirmedExecutions)
	require.Equal(t, "execution_usage_unknown", usage.Reason)
	page, err = s.UsageReconciliationCandidates(t.Context(), back.ID, root.ID, "", 10)
	require.NoError(t, err)
	require.Empty(t, page.Candidates, "ordinary participants do not learn another request's metadata")
	page, err = s.UsageReconciliationCandidates(t.Context(), front.ID, root.ID, "", 10)
	require.NoError(t, err)
	require.Len(t, page.Candidates, 1)
	require.Equal(t, other.ID, page.Candidates[0].MessageID)
}

func TestUsageReconciliationCompletedUnknownSampleAndSuccessiveAttestations(t *testing.T) {
	s, front, back, _ := fixture(t)
	m, _, _ := stoppedUsage(t, s, front, back, "sequential", UsageLimits{}, UsageSample{Usage: providers.Usage{InputTokens: 3}, Reported: false, PriceKnown: false, EstimatedCostUSD: .1})
	// Completion can leave sample uncertainty even when its journal finished.
	_, err := s.db.ExecContext(t.Context(), `UPDATE coordination_usage_runs SET finished=true WHERE message_id=$1`, m.ID)
	require.NoError(t, err)
	_, err = s.db.ExecContext(t.Context(), `UPDATE coordination_messages SET state='completed' WHERE id=$1`, m.ID)
	require.NoError(t, err)
	page, err := s.UsageReconciliationCandidates(t.Context(), front.ID, m.ID, "", 10)
	require.NoError(t, err)
	require.Len(t, page.Candidates, 1)
	require.Zero(t, page.Candidates[0].PendingExecutions)
	require.EqualValues(t, 1, page.Candidates[0].UnknownUsage)
	require.True(t, page.Candidates[0].CanReconcile)
	first := reconciliationInput(t, s, front.ID, m)
	first.InputTokens = 10
	first.CostUSD = .2
	one, err := s.ReconcileTaskUsage(t.Context(), front.ID, first)
	require.NoError(t, err)
	require.EqualValues(t, 10, one.Usage.InputTokens)
	require.False(t, one.Usage.RequiresReview)
	// Simulate a separately admitted later attempt's immutable journal/sample.
	// Old resolution links must not hide newly added evidence.
	raw, _ := json.Marshal(UsageSample{Usage: providers.Usage{InputTokens: 2}, Reported: false, PriceKnown: false, EstimatedCostUSD: .5})
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO coordination_usage_runs(message_id,owner_id) VALUES($1,$2)`, m.ID, uuid.NewString())
	require.NoError(t, err)
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO coordination_usage_samples(project_id,scope,message_id,sample_key,sample) VALUES($1,$2,$3,'later',$4)`, m.ProjectID, usageScope(m), m.ID, raw)
	require.NoError(t, err)
	_, err = s.db.ExecContext(t.Context(), `UPDATE coordination_task_usage SET input_tokens=input_tokens+2,cost_nano=cost_nano+500000000,unknown_usage=unknown_usage+1,unknown_price=unknown_price+1,revision=revision+1 WHERE project_id=$1 AND scope=$2`, m.ProjectID, usageScope(m))
	require.NoError(t, err)
	next, err := s.UsageReconciliationForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.True(t, next.CanReconcile)
	require.EqualValues(t, 12, next.Contribution.InputTokens)
	require.InDelta(t, .2, next.KnownCostFloorUSD, 1e-9)
	require.EqualValues(t, 1, next.PreviousCount)
	second := reconciliationInput(t, s, front.ID, m)
	second.InputTokens = 12
	second.CostUSD = .3
	two, err := s.ReconcileTaskUsage(t.Context(), front.ID, second)
	require.NoError(t, err)
	require.EqualValues(t, 12, two.Usage.InputTokens)
	require.InDelta(t, .3, two.Usage.EstimatedCostUSD, 1e-9)
	require.Zero(t, two.Usage.UnknownUsage)
	require.Zero(t, two.Usage.UnconfirmedExecutions)
	require.EqualValues(t, 2, two.Snapshot.PreviousCount)
	var originalRuns int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_usage_runs WHERE message_id=$1`, m.ID).Scan(&originalRuns))
	require.Equal(t, 2, originalRuns)
}
func TestUsageBudgetComparisonDoesNotOverflow(t *testing.T) {
	usage := TaskUsage{Limits: UsageLimits{Tokens: 1_000_000_000_000}, InputTokens: 9_000_000_000_000_000_000, OutputTokens: 9_000_000_000_000_000_000}
	require.Equal(t, "token_limit", usageReason(usage))
}

func TestUsageReconciliationOwnerDiscoversHiddenChildWithoutPayloadAccess(t *testing.T) {
	s, front, back, _ := fixture(t)
	third, _, err := s.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: "delegate", Role: "worker", Workspace: t.TempDir()})
	require.NoError(t, err)
	input := request("parent for private child")
	input.TaskID = "delegated-task"
	root, err := s.SendWithBudget(t.Context(), front.ID, input, UsageLimits{Tokens: 100})
	require.NoError(t, err)
	parentCtx, _, parentLease := claimLineage(t, s, back)
	defer s.ReleaseLease(t.Context(), parentLease)
	require.NoError(t, s.BeginTaskUsage(parentCtx, root.ID))
	child, err := s.Send(parentCtx, back.ID, SendInput{To: third.Name, Kind: "request", Payload: json.RawMessage(`{"private":"not returned in reconciliation metadata"}`), IdempotencyKey: "hidden child"})
	require.NoError(t, err)
	childCtx, _, childLease := claimLineage(t, s, third)
	require.NoError(t, s.BeginTaskUsage(childCtx, child.ID))
	require.ErrorIs(t, s.RecordTaskUsage(childCtx, child.ID, "missing", UsageSample{Usage: providers.Usage{InputTokens: 2}, Reported: false, PriceKnown: false}), ErrUsageBudget)
	current, err := s.Read(t.Context(), third.ID, child.ID)
	require.NoError(t, err)
	child, err = s.WorkerTransition(t.Context(), childLease, child.ID, current.Revision, "blocked", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.NoError(t, s.ReleaseLease(t.Context(), childLease))
	_, err = s.Read(t.Context(), front.ID, child.ID)
	require.ErrorIs(t, err, ErrNotFound, "ordinary payload access remains participant-filtered")
	page, err := s.UsageReconciliationCandidates(t.Context(), front.ID, root.ID, "", 50)
	require.NoError(t, err)
	found := false
	for _, candidate := range page.Candidates {
		if candidate.MessageID == child.ID {
			found = true
			require.True(t, candidate.CanReconcile)
		}
	}
	require.True(t, found)
	snapshot, err := s.UsageReconciliationForMessage(t.Context(), front.ID, child.ID)
	require.NoError(t, err)
	require.True(t, snapshot.CanReconcile)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "not returned in reconciliation metadata")
	result, err := s.ReconcileTaskUsage(t.Context(), front.ID, reconciliationInput(t, s, front.ID, child))
	require.NoError(t, err)
	require.Zero(t, result.Usage.UnconfirmedExecutions)
	require.EqualValues(t, 1, result.Usage.PendingExecutions, "the still-live parent journal is untouched")
	require.ErrorIs(t, s.RecordTaskUsage(childCtx, child.ID, "late", UsageSample{Reported: true, PriceKnown: true}), ErrLeaseLost)
}

func TestUsageReconciliationRollbackLeavesOriginalEvidenceAndAllowsExactRetry(t *testing.T) {
	s, front, back, _ := fixture(t)
	m, _, _ := stoppedUsage(t, s, front, back, "rollback", UsageLimits{Tokens: 100}, UsageSample{Usage: providers.Usage{InputTokens: 4}, Reported: false, PriceKnown: false})
	in := reconciliationInput(t, s, front.ID, m)
	in.InputTokens = 8
	in.CostUSD = .01
	before, err := s.UsageForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	_, err = s.db.ExecContext(t.Context(), `CREATE FUNCTION reject_reconciliation_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.type='usage_reconciled' THEN RAISE EXCEPTION 'fixture rejects final audit write'; END IF; RETURN NEW; END $$;CREATE TRIGGER reject_reconciliation_audit BEFORE INSERT ON coordination_events FOR EACH ROW EXECUTE FUNCTION reject_reconciliation_audit()`)
	require.NoError(t, err)
	_, err = s.ReconcileTaskUsage(t.Context(), front.ID, in)
	require.Error(t, err)
	after, err := s.UsageForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	original, err := s.Read(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, m.Revision, original.Revision)
	require.Equal(t, Blocked, original.State)
	for _, table := range []string{"coordination_usage_reconciliations", "coordination_usage_run_resolutions", "coordination_usage_sample_resolutions"} {
		var count int
		require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count, table)
	}
	_, err = s.db.ExecContext(t.Context(), `DROP TRIGGER reject_reconciliation_audit ON coordination_events`)
	require.NoError(t, err)
	result, err := s.ReconcileTaskUsage(t.Context(), front.ID, in)
	require.NoError(t, err)
	require.EqualValues(t, 8, result.Usage.InputTokens)
	require.Zero(t, result.Usage.UnconfirmedExecutions)
}

func TestUsageReconciliationSameDispatcherReplayStartsFreshUnconfirmedJournal(t *testing.T) {
	s, front, back, _ := fixture(t)
	bindFixture(t, s, back)
	in := request("dispatcher reconciled replay")
	m, err := s.SendWithBudget(t.Context(), front.ID, in, UsageLimits{Tokens: 100})
	require.NoError(t, err)
	owners := make(chan string, 3)
	executor := testExecutor{reserve: func(ctx context.Context, binding Binding) (Execution, error) {
		return &testExecution{run: func(ctx context.Context, message Message) (json.RawMessage, error) {
			if err := s.BeginTaskUsage(ctx, message.ID); err != nil {
				return nil, err
			}
			lineage := ctx.Value(executionContextKey{}).(executionLineage)
			owners <- lineage.lease.OwnerID
			return nil, ErrUsageBudget
		}}, nil
	}}
	_, _ = startDispatcher(t, s, executor, nil)
	first := waitState(t, s, front.ID, m.ID, Blocked)
	require.Eventually(t, func() bool {
		var busy bool
		err := s.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM coordination_leases WHERE peer_id=$1)`, back.ID).Scan(&busy)
		return err == nil && !busy
	}, 4*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return len(owners) > 0 }, 4*time.Second, 10*time.Millisecond)
	firstOwner := <-owners
	result, err := s.ReconcileTaskUsage(t.Context(), front.ID, reconciliationInput(t, s, front.ID, first))
	require.NoError(t, err)
	review := ReviewRequestInput{MessageID: m.ID, ExpectedRevision: result.Snapshot.MessageRevision, ExpectedUsageRevision: result.Usage.Revision, ReviewID: uuid.NewString(), Note: "Explicitly authorize another attempt after reconciliation", Limits: UsageLimits{Tokens: 100}, Requeue: true}
	_, err = s.ReviewRequest(t.Context(), front.ID, review)
	require.NoError(t, err)
	waitState(t, s, front.ID, m.ID, Blocked)
	require.Eventually(t, func() bool {
		var busy bool
		err := s.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM coordination_leases WHERE peer_id=$1)`, back.ID).Scan(&busy)
		return err == nil && !busy
	}, 4*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return len(owners) > 0 }, 4*time.Second, 10*time.Millisecond)
	secondOwner := <-owners
	require.NotEqual(t, firstOwner, secondOwner)
	usage, err := s.UsageForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, usage.PendingExecutions)
	require.EqualValues(t, 1, usage.UnconfirmedExecutions)
	require.True(t, usage.RequiresReview)
	snapshot, err := s.UsageReconciliationForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, snapshot.RunCount)
	require.EqualValues(t, 1, snapshot.PreviousCount)
	var resolved int
	for _, run := range snapshot.Runs {
		if run.Resolved {
			resolved++
		}
	}
	require.Equal(t, 1, resolved)
}

func TestUsageJournalRefusesReopeningFinishedOwner(t *testing.T) {
	s, front, back, _ := fixture(t)
	_, err := s.Send(t.Context(), front.ID, request("finished owner defense"))
	require.NoError(t, err)
	ctx, m, lease := claimLineage(t, s, back)
	defer s.ReleaseLease(t.Context(), lease)
	require.NoError(t, s.BeginTaskUsage(ctx, m.ID))
	require.NoError(t, s.BeginTaskUsage(ctx, m.ID), "same unfinished Begin remains idempotent")
	require.NoError(t, s.FinishTaskUsage(ctx, m.ID))
	require.ErrorIs(t, s.BeginTaskUsage(ctx, m.ID), ErrConflict)
}

func TestUsageReconciliationWaitsForUncommittedLeaseRenewal(t *testing.T) {
	s, front, back, _ := fixture(t)
	m, _, _ := stoppedUsage(t, s, front, back, "renewal-race", UsageLimits{Tokens: 100})
	in := reconciliationInput(t, s, front.ID, m)
	before, err := s.UsageForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO coordination_leases(peer_id,owner_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '300 milliseconds')`, back.ID, uuid.NewString())
	require.NoError(t, err)
	renewal, err := s.db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer renewal.Rollback()
	var blockerPID int
	require.NoError(t, renewal.QueryRowContext(t.Context(), `SELECT pg_backend_pid()`).Scan(&blockerPID))
	_, err = renewal.ExecContext(t.Context(), `UPDATE coordination_leases SET expires_at=clock_timestamp()+interval '30 seconds' WHERE peer_id=$1 AND expires_at>clock_timestamp()`, back.ID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		var expired bool
		err := s.db.QueryRowContext(t.Context(), `SELECT expires_at<=clock_timestamp() FROM coordination_leases WHERE peer_id=$1`, back.ID).Scan(&expired)
		return err == nil && expired
	}, 3*time.Second, 10*time.Millisecond)
	done := make(chan error, 1)
	go func() { _, err := s.ReconcileTaskUsage(t.Context(), front.ID, in); done <- err }()
	require.Eventually(t, func() bool {
		var blocked bool
		err := s.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE 'SELECT owner_id FROM coordination_leases%')`, blockerPID).Scan(&blocked)
		return err == nil && blocked
	}, 3*time.Second, 10*time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("reconciliation passed a pending renewal: %v", err)
	default:
	}
	require.NoError(t, renewal.Commit())
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrBusy)
	case <-time.After(3 * time.Second):
		t.Fatal("reconciliation did not finish after renewal committed")
	}
	after, err := s.UsageForMessage(t.Context(), front.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_usage_reconciliations`).Scan(&count))
	require.Zero(t, count)
}
