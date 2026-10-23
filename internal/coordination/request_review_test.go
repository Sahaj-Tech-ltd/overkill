package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func blockedReview(t *testing.T, s *Store, from, to Peer, limits UsageLimits) (Message, ReviewRequestInput) {
	t.Helper()
	in := request(uuid.NewString())
	in.To = to.Name
	m, err := s.SendWithBudget(t.Context(), from.ID, in, limits)
	require.NoError(t, err)
	m, err = s.Claim(t.Context(), to.ID)
	require.NoError(t, err)
	m, err = s.Transition(t.Context(), to.ID, m.ID, m.Revision, "blocked", json.RawMessage(`{"reason":"review"}`))
	require.NoError(t, err)
	u, err := s.UsageForMessage(t.Context(), from.ID, m.ID)
	require.NoError(t, err)
	return m, ReviewRequestInput{MessageID: m.ID, ExpectedRevision: m.Revision, ExpectedUsageRevision: u.Revision, ReviewID: uuid.NewString(), Note: "Reviewed result and possible repeated effects", Limits: limits, Requeue: true}
}
func TestReviewRequestAtomicBudgetReceiptRestartAndWorkerClaim(t *testing.T) {
	s, from, back, _ := fixture(t)
	m, in := blockedReview(t, s, from, back, UsageLimits{Tokens: 20, CostUSD: .1})
	// Populate durable confirmed totals to model the accounting that caused the block.
	_, err := s.db.ExecContext(t.Context(), `UPDATE coordination_task_usage SET input_tokens=20,output_tokens=2,cached_tokens=3,cost_nano=10000000,review_reason='token_limit' WHERE project_id=$1 AND scope=$2`, m.ProjectID, usageScope(m))
	require.NoError(t, err)
	_, err = s.ReviewRequest(t.Context(), from.ID, in)
	require.ErrorIs(t, err, ErrUsageBudget)
	in.Limits = UsageLimits{Tokens: 100, CostUSD: 1}
	got, err := s.ReviewRequest(t.Context(), from.ID, in)
	require.NoError(t, err)
	require.Equal(t, Accepted, got.Message.State)
	require.Equal(t, m.ExecutionAttempts, got.Message.ExecutionAttempts)
	require.Equal(t, m.Deadline, got.Message.Deadline)
	require.EqualValues(t, 20, got.Usage.InputTokens)
	require.EqualValues(t, 3, got.Usage.CachedInputTokens)
	require.Equal(t, from.ID, got.Usage.OwnerPeerID)
	require.False(t, got.Usage.RequiresReview)
	restarted, err := NewStore(t.Context(), s.db)
	require.NoError(t, err)
	claimed, err := restarted.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, m.ID, claimed.ID)
	require.Equal(t, m.ExecutionAttempts+1, claimed.ExecutionAttempts)
	again, err := restarted.ReviewRequest(t.Context(), from.ID, in)
	require.NoError(t, err)
	originalJSON, err := json.Marshal(got)
	require.NoError(t, err)
	replayedJSON, err := json.Marshal(again)
	require.NoError(t, err)
	require.JSONEq(t, string(originalJSON), string(replayedJSON), "receipt survives subsequent claim and store restart")
	changed := in
	changed.Note = "different"
	_, err = restarted.ReviewRequest(t.Context(), from.ID, changed)
	require.ErrorIs(t, err, ErrIdempotency)
	ev, err := s.Events(t.Context(), from.ID, 0, 100)
	require.NoError(t, err)
	count := 0
	for _, e := range ev {
		if e.Type == "reviewed" {
			count++
			require.Contains(t, string(e.Payload), in.Note)
		}
	}
	require.Equal(t, 1, count)
	require.NoError(t, s.DisablePeer(t.Context(), from.ID))
	_, err = restarted.ReviewRequest(t.Context(), from.ID, in)
	require.Error(t, err, "disabled actor cannot replay receipt")
}
func TestReviewRequestAuthorityCASAndConcurrentReceipts(t *testing.T) {
	s, from, back, _ := fixture(t)
	m, in := blockedReview(t, s, from, back, UsageLimits{})
	_, err := s.ReviewRequest(t.Context(), back.ID, in)
	require.ErrorIs(t, err, ErrUnauthorized)
	other := dependencyWorker(t, s)
	_, err = s.ReviewRequest(t.Context(), other.ID, in)
	require.ErrorIs(t, err, ErrNotFound)
	stale := in
	stale.ExpectedUsageRevision++
	_, err = s.ReviewRequest(t.Context(), from.ID, stale)
	require.ErrorIs(t, err, ErrConflict)
	stale = in
	stale.ExpectedRevision++
	_, err = s.ReviewRequest(t.Context(), from.ID, stale)
	require.ErrorIs(t, err, ErrConflict)
	var wg sync.WaitGroup
	results := make(chan ReviewRequestResult, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := s.ReviewRequest(t.Context(), from.ID, in); results <- r; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	for r := range results {
		require.Equal(t, m.Revision+1, r.Message.Revision)
	}
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_request_reviews`).Scan(&count))
	require.Equal(t, 1, count)
}
func TestReviewRequestRefusesUnknownEvidenceEvenWithUnlimitedProposal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits UsageLimits
		column string
	}{
		{"usage", UsageLimits{Tokens: 20}, "unknown_usage"}, {"price", UsageLimits{CostUSD: 1}, "unknown_price"}, {"new_cost_limit", UsageLimits{}, "unknown_price"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, from, back, _ := fixture(t)
			m, in := blockedReview(t, s, from, back, tc.limits)
			_, err := s.db.ExecContext(t.Context(), `UPDATE coordination_task_usage SET `+tc.column+`=1 WHERE project_id=$1 AND scope=$2`, m.ProjectID, usageScope(m))
			require.NoError(t, err)
			in.Limits = UsageLimits{}
			if tc.name == "new_cost_limit" {
				in.Limits.CostUSD = 1
			}
			_, err = s.ReviewRequest(t.Context(), from.ID, in)
			require.ErrorIs(t, err, ErrUsageBudget)
			current, err := s.Read(t.Context(), from.ID, m.ID)
			require.NoError(t, err)
			require.Equal(t, m.Revision, current.Revision)
		})
	}
	t.Run("unfinished_journal", func(t *testing.T) {
		s, from, back, _ := fixture(t)
		m, in := blockedReview(t, s, from, back, UsageLimits{})
		_, err := s.db.ExecContext(t.Context(), `INSERT INTO coordination_usage_runs(message_id,owner_id) VALUES($1,$2)`, m.ID, uuid.NewString())
		require.NoError(t, err)
		_, err = s.ReviewRequest(t.Context(), from.ID, in)
		require.ErrorIs(t, err, ErrUsageBudget)
		var unfinished int
		require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_usage_runs WHERE NOT finished`).Scan(&unfinished))
		require.Equal(t, 1, unfinished)
	})
}
func TestReviewRequestDeadlineAttemptAndLiveLeaseFences(t *testing.T) {
	for _, kind := range []string{"deadline", "attempt", "lease"} {
		t.Run(kind, func(t *testing.T) {
			s, from, back, _ := fixture(t)
			m, in := blockedReview(t, s, from, back, UsageLimits{})
			switch kind {
			case "deadline":
				_, err := s.db.ExecContext(t.Context(), `UPDATE coordination_messages SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, m.ID)
				require.NoError(t, err)
			case "attempt":
				_, err := s.db.ExecContext(t.Context(), `UPDATE coordination_messages SET execution_attempts=$2 WHERE id=$1`, m.ID, MaxExecutionAttempts)
				require.NoError(t, err)
			case "lease":
				_, err := s.db.ExecContext(t.Context(), `INSERT INTO coordination_leases(peer_id,owner_id,expires_at) VALUES($1,$2,clock_timestamp()+interval '1 minute')`, back.ID, uuid.NewString())
				require.NoError(t, err)
			}
			_, err := s.ReviewRequest(t.Context(), from.ID, in)
			require.Error(t, err)
			var receipts int
			require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_request_reviews`).Scan(&receipts))
			require.Zero(t, receipts)
		})
	}
}
func TestReviewRequestPreservesDependenciesAndContractsForNextClaim(t *testing.T) {
	s, from, back, _ := fixture(t)
	worker := dependencyWorker(t, s)
	prereq := sendDependency(t, s, from, worker.Name, "prerequisite")
	dep, err := s.Claim(t.Context(), worker.ID)
	require.NoError(t, err)
	finishDependency(t, s, worker, dep)
	c, err := s.CreateContract(t.Context(), from.ID, contractSpec(from, "review-gate"))
	require.NoError(t, err)
	_, err = s.AcknowledgeContract(t.Context(), back.ID, c.ID, c.Revision)
	require.NoError(t, err)
	input := request("original-gates")
	input.DependsOn = []string{prereq.ID}
	input.RequiredContracts = []ContractDependency{{ContractID: c.ID, Revision: c.Revision}}
	m, err := s.Send(t.Context(), from.ID, input)
	require.NoError(t, err)
	m, err = s.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	m, err = s.Transition(t.Context(), back.ID, m.ID, m.Revision, "blocked", json.RawMessage(`{}`))
	require.NoError(t, err)
	u, err := s.UsageForMessage(t.Context(), from.ID, m.ID)
	require.NoError(t, err)
	in := ReviewRequestInput{MessageID: m.ID, ExpectedRevision: m.Revision, ExpectedUsageRevision: u.Revision, ReviewID: uuid.NewString(), Note: "Reviewed without changing original requirements", Requeue: true}
	// Another agreement revision arrives while this request is blocked. Review
	// preserves its original pin, so only a normal claim may apply stale-pin failure.
	_, err = s.UpdateContract(t.Context(), from.ID, c.ID, c.Revision, c.Spec)
	require.NoError(t, err)
	got, err := s.ReviewRequest(t.Context(), from.ID, in)
	require.NoError(t, err)
	require.Equal(t, []string{prereq.ID}, got.Message.DependsOn)
	require.Equal(t, m.RequiredContracts, got.Message.RequiredContracts)
	require.Equal(t, m.ParentMessageID, got.Message.ParentMessageID)
	require.Equal(t, m.RequestDepth, got.Message.RequestDepth)
	_, err = s.Claim(t.Context(), back.ID)
	require.ErrorIs(t, err, ErrNotFound)
	failed, err := s.Read(t.Context(), from.ID, m.ID)
	require.NoError(t, err)
	require.Equal(t, Failed, failed.State)
	require.Equal(t, m.ExecutionAttempts, failed.ExecutionAttempts)
}
func TestReviewRequestConcurrentUsageAndReviewCompareAndSwap(t *testing.T) {
	s, from, back, _ := fixture(t)
	m, err := s.SendWithBudget(t.Context(), from.ID, request("concurrent"), UsageLimits{Tokens: 100})
	require.NoError(t, err)
	ctx, claimed, lease := claimLineage(t, s, back)
	m, err = s.WorkerTransition(t.Context(), lease, claimed.ID, claimed.Revision, "blocked", json.RawMessage(`{}`))
	require.NoError(t, err)
	u, err := s.UsageForMessage(t.Context(), from.ID, m.ID)
	require.NoError(t, err)
	in := ReviewRequestInput{MessageID: m.ID, ExpectedRevision: m.Revision, ExpectedUsageRevision: u.Revision, ReviewID: uuid.NewString(), Note: "Reviewed", Limits: UsageLimits{Tokens: 200}, Requeue: true}
	// A still-live accounting executor prevents review even before it reports.
	_, err = s.ReviewRequest(t.Context(), from.ID, in)
	require.ErrorIs(t, err, ErrBusy)
	require.NoError(t, s.RecordTaskUsage(ctx, m.ID, "late-confirmed", UsageSample{Usage: providers.Usage{InputTokens: 2}, Reported: true, PriceKnown: true}))
	require.NoError(t, s.ReleaseLease(t.Context(), lease))
	_, err = s.ReviewRequest(t.Context(), from.ID, in)
	require.ErrorIs(t, err, ErrConflict)
	u, err = s.UsageForMessage(t.Context(), from.ID, m.ID)
	require.NoError(t, err)
	in.ExpectedUsageRevision = u.Revision
	// Competing distinct reviews from the same snapshot: exactly one commits.
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := in
			copy.ReviewID = uuid.NewString()
			_, e := s.ReviewRequest(t.Context(), from.ID, copy)
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			successes++
		} else if errors.Is(e, ErrConflict) {
			conflicts++
		} else {
			require.NoError(t, e)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
}
func TestReviewRequestInputValidation(t *testing.T) {
	s, from, back, _ := fixture(t)
	_, in := blockedReview(t, s, from, back, UsageLimits{})
	for _, change := range []func(*ReviewRequestInput){func(i *ReviewRequestInput) { i.Requeue = false }, func(i *ReviewRequestInput) { i.Note = " " }, func(i *ReviewRequestInput) { i.ReviewID = "bad" }, func(i *ReviewRequestInput) { i.Limits.Tokens = -1 }, func(i *ReviewRequestInput) { i.Limits.CostUSD = 0.0000000001 }, func(i *ReviewRequestInput) { i.ExpectedRevision = 0 }} {
		bad := in
		change(&bad)
		_, err := s.ReviewRequest(t.Context(), from.ID, bad)
		require.Error(t, err)
	}
}

func TestReviewRequestDescendantOwnerStillNeedsParticipantAccess(t *testing.T) {
	s, from, back, _ := fixture(t)
	worker := dependencyWorker(t, s)
	_, err := s.Send(t.Context(), from.ID, request("root-owner"))
	require.NoError(t, err)
	ctx, parent, lease := claimLineage(t, s, back)
	defer s.ReleaseLease(t.Context(), lease)
	child, err := s.Send(ctx, back.ID, SendInput{To: worker.Name, Kind: "request", Payload: json.RawMessage(`{}`), IdempotencyKey: "owned-child"})
	require.NoError(t, err)
	child, err = s.Claim(t.Context(), worker.ID)
	require.NoError(t, err)
	child, err = s.Transition(t.Context(), worker.ID, child.ID, child.Revision, "blocked", json.RawMessage(`{}`))
	require.NoError(t, err)
	u, err := s.UsageForMessage(t.Context(), back.ID, child.ID)
	require.NoError(t, err)
	require.Equal(t, from.ID, u.OwnerPeerID)
	in := ReviewRequestInput{MessageID: child.ID, ExpectedRevision: child.Revision, ExpectedUsageRevision: u.Revision, ReviewID: uuid.NewString(), Note: "Requires owner and correspondence", Requeue: true}
	_, err = s.ReviewRequest(t.Context(), from.ID, in)
	require.ErrorIs(t, err, ErrNotFound, "owner cannot inspect non-correspondent descendant")
	_, err = s.ReviewRequest(t.Context(), back.ID, in)
	require.ErrorIs(t, err, ErrUnauthorized, "child sender cannot change root owner's limits")
	_, err = s.ReviewRequest(t.Context(), worker.ID, in)
	require.ErrorIs(t, err, ErrUnauthorized)
	require.Equal(t, parent.ID, child.ParentMessageID)
}

func TestReviewRequestSerializesRealLeaseRecoveryAndReview(t *testing.T) {
	s, from, back, _ := fixture(t)
	m, in := blockedReview(t, s, from, back, UsageLimits{})
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var reviewed ReviewRequestResult
	var reviewErr, leaseErr error
	var lease Lease
	go func() { defer wg.Done(); <-start; reviewed, reviewErr = s.ReviewRequest(t.Context(), from.ID, in) }()
	go func() {
		defer wg.Done()
		<-start
		lease, leaseErr = s.AcquireLease(t.Context(), back.ID, uuid.NewString(), time.Minute)
	}()
	close(start)
	wg.Wait()
	require.NoError(t, reviewErr)
	require.Equal(t, Accepted, reviewed.Message.State)
	if leaseErr == nil {
		claimed, err := s.ClaimLease(t.Context(), lease)
		require.NoError(t, err)
		require.Equal(t, m.ID, claimed.ID)
		require.NoError(t, s.ReleaseLease(t.Context(), lease))
	} else {
		require.ErrorIs(t, leaseErr, ErrRecoveryRequired)
		claimed, err := s.Claim(t.Context(), back.ID)
		require.NoError(t, err)
		require.Equal(t, m.ID, claimed.ID)
	}
}

func TestReviewRequestLegacyResumeSharesProjectAdmission(t *testing.T) {
	s, from, back, _ := fixture(t)
	worker := dependencyWorker(t, s)
	root, err := s.Send(t.Context(), from.ID, request("resume-root"))
	require.NoError(t, err)
	ctx, root, lease := claimLineage(t, s, back)
	child, err := s.Send(ctx, back.ID, SendInput{To: worker.Name, Kind: "request", Payload: json.RawMessage(`{}`), IdempotencyKey: "resume-child"})
	require.NoError(t, err)
	child, err = s.Claim(t.Context(), worker.ID)
	require.NoError(t, err)
	child, err = s.Transition(t.Context(), worker.ID, child.ID, child.Revision, "blocked", json.RawMessage(`{}`))
	require.NoError(t, err)
	root, err = s.WorkerTransition(t.Context(), lease, root.ID, root.Revision, "blocked", json.RawMessage(`{}`))
	require.NoError(t, err)
	require.NoError(t, s.ReleaseLease(t.Context(), lease))
	u, err := s.UsageForMessage(t.Context(), from.ID, root.ID)
	require.NoError(t, err)
	in := ReviewRequestInput{MessageID: root.ID, ExpectedRevision: root.Revision, ExpectedUsageRevision: u.Revision, ReviewID: uuid.NewString(), Note: "Reviewed shared task", Requeue: true}
	// The task's legacy recipient resume must wait for the exact admission lock
	// that serializes its owner's review, even though they lock different peers.
	held, err := s.db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer held.Rollback()
	require.NoError(t, lockAdmission(t.Context(), held, from.ID))
	limited, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, err = s.Transition(limited, worker.ID, child.ID, child.Revision, "started", json.RawMessage(`{}`))
	cancel()
	require.Error(t, err)
	unchanged, err := s.Read(t.Context(), worker.ID, child.ID)
	require.NoError(t, err)
	require.Equal(t, Blocked, unchanged.State)
	require.NoError(t, held.Rollback())
	// Race actual different-peer resume and review. If resume wins the serialized
	// admission order, review refuses the active shared task. If review wins, the
	// normal later resume observes the new limits without changing either counter.
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var reviewErr, resumeErr error
	go func() { defer wg.Done(); <-start; _, reviewErr = s.ReviewRequest(t.Context(), from.ID, in) }()
	go func() {
		defer wg.Done()
		<-start
		_, resumeErr = s.Transition(t.Context(), worker.ID, child.ID, child.Revision, "started", json.RawMessage(`{}`))
	}()
	close(start)
	wg.Wait()
	require.NoError(t, resumeErr)
	if reviewErr != nil {
		require.ErrorIs(t, reviewErr, ErrBusy)
	}
	after, err := s.UsageForMessage(t.Context(), from.ID, root.ID)
	require.NoError(t, err)
	require.Zero(t, after.InputTokens)
	require.Zero(t, after.OutputTokens)
}
