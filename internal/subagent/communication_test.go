package subagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func communicationFixture(t *testing.T) *CommunicationStore {
	t.Helper()
	db, _ := testpg.Open(t)
	s, err := NewCommunicationStore(t.Context(), db, "chat-one")
	require.NoError(t, err)
	require.NoError(t, s.RegisterBatch(t.Context(), []WorkerRecord{{ID: "front-id", Name: "frontend", Goal: "Build UI", Tools: []string{"fs"}}, {ID: "back-id", Name: "backend", Goal: "Build API", Tools: []string{"fs"}}}))
	return s
}

func TestWorkerMessagesDedupeScopeCorrelationAndAdmission(t *testing.T) {
	s := communicationFixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	failures := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			m, err := s.Send(t.Context(), "front-id", "backend", "question", "What is the schema?", "schema-v1", "")
			ids <- m.ID
			failures <- err
		})
	}
	wg.Wait()
	close(ids)
	close(failures)
	var id string
	for got := range ids {
		if id == "" {
			id = got
		}
		require.Equal(t, id, got)
	}
	for err := range failures {
		require.NoError(t, err)
	}
	_, err := s.Send(t.Context(), "front-id", "backend", "question", "different", "schema-v1", "")
	require.ErrorIs(t, err, ErrMessageConflict)
	_, err = s.Send(t.Context(), "forged", "backend", "update", "hi", "forged", "")
	require.ErrorIs(t, err, ErrWorkerUnavailable)
	_, err = s.Send(t.Context(), "front-id", "frontend", "update", "self", "self", "")
	require.Error(t, err)
	_, err = s.Send(t.Context(), "back-id", "frontend", "answer", "schema", "bad-reply", "foreign-id")
	require.Error(t, err)
	got, err := s.Inbox(t.Context(), "back-id", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "accepted", got[0].State)
	require.Error(t, s.Acknowledge(t.Context(), "front-id", id))
	require.Error(t, s.Acknowledge(t.Context(), "back-id", id), "acceptance is not admission")
	err = s.Admit(t.Context(), "back-id", func(batch []WorkerMessage) error {
		require.Len(t, batch, 1)
		return errors.New("history not available")
	})
	require.Error(t, err)
	got, err = s.Inbox(t.Context(), "back-id", 0)
	require.NoError(t, err)
	require.Equal(t, "accepted", got[0].State)
	require.NoError(t, s.Admit(t.Context(), "back-id", func(batch []WorkerMessage) error { require.Equal(t, id, batch[0].ID); return nil }))
	require.NoError(t, s.Acknowledge(t.Context(), "back-id", id))
	answer, err := s.Send(t.Context(), "back-id", "front-id", "answer", "GET /users returns id:string", "answer-v1", id)
	require.NoError(t, err)
	require.Equal(t, id, answer.InReplyTo)
	other, err := NewCommunicationStore(t.Context(), s.db, "different-chat")
	require.NoError(t, err)
	_, err = other.Send(t.Context(), ParentWorker, "back-id", "update", "foreign", "outside", "")
	require.ErrorIs(t, err, ErrWorkerUnavailable)
	foreign, err := other.Inbox(t.Context(), ParentWorker, 0)
	require.NoError(t, err)
	require.Empty(t, foreign)
}

func TestWorkerMessagesRetainHistoryAcrossRestartWithoutRevivingWorkers(t *testing.T) {
	s := communicationFixture(t)
	queued, err := s.Send(t.Context(), "front-id", ParentWorker, "update", "Built frontend", "update-parent", "")
	require.NoError(t, err)
	restarted, err := NewCommunicationStore(t.Context(), s.db, s.scope)
	require.NoError(t, err)
	workers, err := restarted.Workers(t.Context(), true)
	require.NoError(t, err)
	require.Len(t, workers, 2)
	require.False(t, workers[0].Live, "old running status is unobserved, not a live process claim")
	_, err = restarted.Send(t.Context(), ParentWorker, "back-id", "update", "do not resurrect", "old-target", "")
	require.ErrorIs(t, err, ErrWorkerUnavailable)
	_, err = restarted.Send(t.Context(), "front-id", ParentWorker, "update", "forged incarnation", "old-source", "")
	require.ErrorIs(t, err, ErrWorkerUnavailable)
	got, err := restarted.Inbox(t.Context(), ParentWorker, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, queued.ID, got[0].ID)
	require.NoError(t, restarted.Admit(t.Context(), ParentWorker, func(messages []WorkerMessage) error { require.Equal(t, queued.ID, messages[0].ID); return nil }))
	got, err = restarted.Inbox(t.Context(), ParentWorker, 0)
	require.NoError(t, err)
	require.Equal(t, "admitted", got[0].State)
	require.NoError(t, s.Finish(context.Background(), "back-id", &Result{Status: "completed", Summary: "Done"}))
	_, err = s.Send(t.Context(), ParentWorker, "backend", "update", "new work?", "finished", "")
	require.ErrorIs(t, err, ErrWorkerUnavailable)
}

func TestWorkerBatchRegistrationIsAtomicAndLabelsAreBounded(t *testing.T) {
	s := communicationFixture(t)
	err := s.RegisterBatch(t.Context(), []WorkerRecord{{ID: "new-id", Name: "new-worker", Goal: "first"}, {ID: "duplicate", Name: "backend", Goal: "must not start"}})
	require.Error(t, err)
	workers, err := s.Workers(t.Context(), false)
	require.NoError(t, err)
	require.Len(t, workers, 2)
	for _, name := range []string{"parent", "UPPER", "../outside"} {
		require.Error(t, s.RegisterBatch(t.Context(), []WorkerRecord{{ID: "invalid", Name: name, Goal: "no"}}))
	}
}

func TestRecentWorkerInboxRefreshesDeliveryStatesPastFirstPage(t *testing.T) {
	s := communicationFixture(t)
	for i := 0; i < 102; i++ {
		_, err := s.Send(t.Context(), "front-id", ParentWorker, "update", fmt.Sprintf("progress-%03d", i), fmt.Sprintf("key-%d", i), "")
		require.NoError(t, err)
	}
	first, err := s.Inbox(t.Context(), ParentWorker, 0)
	require.NoError(t, err)
	require.Len(t, first, 100)
	require.Equal(t, "progress-099", first[99].Content)
	latest, err := s.RecentInbox(t.Context(), ParentWorker)
	require.NoError(t, err)
	require.Len(t, latest, 100)
	require.Equal(t, "progress-002", latest[0].Content)
	require.Equal(t, "progress-101", latest[99].Content)
	require.NoError(t, s.Admit(t.Context(), ParentWorker, func([]WorkerMessage) error { return nil }))
	latest, err = s.RecentInbox(t.Context(), ParentWorker)
	require.NoError(t, err)
	require.Equal(t, "admitted", latest[0].State)
	require.Equal(t, "accepted", latest[99].State)
	require.Less(t, latest[0].Sequence, latest[99].Sequence)
}

func TestWorkerListBoundsSummariesAndKeepsPrivateEvidenceForParentFollowup(t *testing.T) {
	s := communicationFixture(t)
	require.NoError(t, s.RegisterBatch(t.Context(), []WorkerRecord{{ID: "long-worker", Name: "long-worker", Goal: strings.Repeat("g", 16000), Tools: []string{"fs"}}}))
	require.NoError(t, s.Finish(t.Context(), "long-worker", &Result{Status: "completed", Summary: strings.Repeat("evidence", 2000), ToolTrace: []ToolEntry{{Tool: "fs", Path: "private-evidence-path"}}}))
	workers, err := s.Workers(t.Context(), false)
	require.NoError(t, err)
	w := workers[0]
	require.Equal(t, "long-worker", w.ID)
	require.True(t, w.GoalTruncated)
	require.True(t, w.SummaryOnly)
	require.Less(t, len(w.Goal), 1030)
	require.Less(t, len(w.Result.Summary), 4100)
	require.Empty(t, w.Result.ToolTrace)
	require.Empty(t, w.Tools)
	full, err := s.ReadWorker(t.Context(), w.ID)
	require.NoError(t, err)
	require.Equal(t, 16000, len(full.Goal))
	require.Len(t, full.Result.ToolTrace, 1)
	require.Equal(t, []string{"fs"}, full.Tools)
}

func TestAdmissionCommitRecoveryDistinguishesCommittedRollbackAndUnresolved(t *testing.T) {
	s := communicationFixture(t)
	message, err := s.Send(t.Context(), "front-id", "backend", "update", "current schema", "commit-probe", "")
	require.NoError(t, err)
	lost := errors.New("COMMIT response lost")
	require.ErrorIs(t, s.resolveAdmissionCommit("back-id", []WorkerMessage{message}, lost), lost, "confirmed rollback is safe to retry")
	require.NoError(t, s.Admit(t.Context(), "back-id", func([]WorkerMessage) error { return nil }))
	require.NoError(t, s.resolveAdmissionCommit("back-id", []WorkerMessage{message}, lost), "confirmed durable commit keeps the appended context")
	unknown := message
	unknown.ID = "no-such-message"
	require.ErrorIs(t, s.resolveAdmissionCommit("back-id", []WorkerMessage{unknown}, lost), ErrMessageAdmissionUncertain)
	require.ErrorIs(t, s.resolveAdmissionCommit("front-id", []WorkerMessage{message}, lost), ErrMessageAdmissionUncertain, "cannot reconcile another recipient's batch")
}
