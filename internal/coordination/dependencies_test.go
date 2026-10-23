package coordination

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func dependencyWorker(t *testing.T, s *Store) Peer {
	t.Helper()
	p, _, err := s.RegisterPeer(t.Context(), Peer{ProjectID: "project", Name: "worker", Role: "dependency", Workspace: t.TempDir()})
	require.NoError(t, err)
	return p
}
func sendDependency(t *testing.T, s *Store, from Peer, to, key string, ids ...string) Message {
	t.Helper()
	in := request(key)
	in.To = to
	in.DependsOn = ids
	m, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	return m
}
func finishDependency(t *testing.T, s *Store, peer Peer, m Message) Message {
	t.Helper()
	done, err := s.Transition(t.Context(), peer.ID, m.ID, m.Revision, "completed", json.RawMessage(`{}`))
	require.NoError(t, err)
	return done
}

func TestDependenciesGateWithoutStarvingIndependentWorkAndSurviveRestart(t *testing.T) {
	s, from, backend, _ := fixture(t)
	worker := dependencyWorker(t, s)
	prereq := sendDependency(t, s, from, "worker", "prereq")
	dependent := sendDependency(t, s, from, "backend", "dependent", prereq.ID)
	independent := sendDependency(t, s, from, "backend", "independent")
	// A second Store is a fresh runtime over the durable database, not an in-memory graph.
	restarted, err := NewStore(t.Context(), s.db)
	require.NoError(t, err)
	got, err := restarted.Claim(t.Context(), backend.ID)
	require.NoError(t, err)
	require.Equal(t, independent.ID, got.ID)
	finishDependency(t, restarted, backend, got)
	_, err = restarted.Claim(t.Context(), backend.ID)
	require.ErrorIs(t, err, ErrNotFound)
	g, err := restarted.Graph(t.Context(), from.ID, dependent.ID, 100)
	require.NoError(t, err)
	require.Len(t, g.Nodes, 2)
	require.Equal(t, "waiting", g.Nodes[0].Readiness)
	// Actual independent recipient claims can run concurrently; the waiting request cannot sneak through.
	var wg sync.WaitGroup
	wg.Add(2)
	var started Message
	var claimErr, waitingErr error
	go func() { defer wg.Done(); started, claimErr = restarted.Claim(t.Context(), worker.ID) }()
	go func() { defer wg.Done(); _, waitingErr = restarted.Claim(t.Context(), backend.ID) }()
	wg.Wait()
	require.NoError(t, claimErr)
	require.ErrorIs(t, waitingErr, ErrNotFound)
	require.Equal(t, prereq.ID, started.ID)
	finishDependency(t, restarted, worker, started)
	g, err = restarted.Graph(t.Context(), from.ID, dependent.ID, 100)
	require.NoError(t, err)
	require.Equal(t, "ready", g.Nodes[0].Readiness)
	got, err = restarted.Claim(t.Context(), backend.ID)
	require.NoError(t, err)
	require.Equal(t, dependent.ID, got.ID)
	require.Equal(t, []string{prereq.ID}, got.DependsOn)
	require.Equal(t, 1, got.ExecutionAttempts)
}

func TestDependenciesScopeGraphPrivacyAndTruncation(t *testing.T) {
	s, from, backend, _ := fixture(t)
	dependencyWorker(t, s)
	prereq := sendDependency(t, s, from, "worker", "private-prereq")
	dependent := sendDependency(t, s, from, "backend", "visible-dependent", prereq.ID)
	graph, err := s.Graph(t.Context(), backend.ID, dependent.ID, 100)
	require.NoError(t, err)
	require.True(t, graph.HiddenDependencies)
	require.False(t, graph.Truncated)
	require.Len(t, graph.Nodes, 1)
	require.JSONEq(t, `null`, string(graph.Nodes[0].Message.Payload))
	require.Equal(t, Accepted, graph.Nodes[0].Blockers[0].State)
	require.Equal(t, prereq.ID, graph.Nodes[0].Blockers[0].ID)
	_, err = s.Graph(t.Context(), backend.ID, prereq.ID, 100)
	require.ErrorIs(t, err, ErrNotFound)
	graph, err = s.Graph(t.Context(), from.ID, dependent.ID, 1)
	require.NoError(t, err)
	require.True(t, graph.Truncated)
	require.Len(t, graph.Nodes, 1)
	require.False(t, graph.HiddenDependencies)
	outsider, _, err := s.RegisterPeer(t.Context(), Peer{ProjectID: "other", Name: "outsider", Role: "other", Workspace: t.TempDir()})
	require.NoError(t, err)
	_, err = s.Graph(t.Context(), outsider.ID, dependent.ID, 100)
	require.ErrorIs(t, err, ErrNotFound)
	for _, limit := range []int{-1, 201} {
		_, err = s.Graph(t.Context(), from.ID, dependent.ID, limit)
		require.Error(t, err)
	}
}

func TestDependenciesValidateScopeAndIdempotency(t *testing.T) {
	s, from, backend, _ := fixture(t)
	dependencyWorker(t, s)
	prereq := sendDependency(t, s, from, "worker", "prereq")
	in := request("depends")
	in.DependsOn = []string{prereq.ID}
	first, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	repeat, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	require.Equal(t, first.ID, repeat.ID)
	in.DependsOn = nil
	_, err = s.Send(t.Context(), from.ID, in)
	require.ErrorIs(t, err, ErrIdempotency)
	// A recipient cannot use a prerequisite in which it has no participation.
	in = request("unauthorized-dep")
	in.To = "worker"
	in.DependsOn = []string{prereq.ID}
	_, err = s.Send(t.Context(), backend.ID, in)
	require.ErrorIs(t, err, ErrNotFound)
	// Unknown/future/self IDs cannot create edges, and only requests are prerequisites.
	for _, ids := range [][]string{{uuid.NewString()}, {"bad"}, {prereq.ID, prereq.ID}, {""}} {
		in = request(uuid.NewString())
		in.DependsOn = ids
		_, err = s.Send(t.Context(), from.ID, in)
		require.Error(t, err)
	}
	in = request("non-request")
	in.Kind = "progress"
	notice, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	graph, err := s.Graph(t.Context(), from.ID, notice.ID, 100)
	require.NoError(t, err)
	require.Len(t, graph.Nodes, 1)
	require.Equal(t, "ready", graph.Nodes[0].Readiness)
	require.Empty(t, graph.Nodes[0].ContractBlockers)
	in = request("bad-prereq")
	in.DependsOn = []string{notice.ID}
	_, err = s.Send(t.Context(), from.ID, in)
	require.ErrorIs(t, err, ErrNotFound)
	in = request("notification-deps")
	in.Kind = "progress"
	in.DependsOn = []string{prereq.ID}
	_, err = s.Send(t.Context(), from.ID, in)
	require.Error(t, err)
	in = request("oversize")
	for range 33 {
		in.DependsOn = append(in.DependsOn, uuid.NewString())
	}
	_, err = s.Send(t.Context(), from.ID, in)
	require.Error(t, err)
	// Even a known ID from another project is unavailable.
	other, _, err := s.RegisterPeer(t.Context(), Peer{ProjectID: "other", Name: "other", Role: "other", Workspace: t.TempDir()})
	require.NoError(t, err)
	in = request("cross-project")
	in.DependsOn = []string{prereq.ID}
	require.ErrorIs(t, authorizeDependenciesForTest(t, s, other, in.DependsOn), ErrNotFound)
}
func authorizeDependenciesForTest(t *testing.T, s *Store, p Peer, ids []string) error {
	t.Helper()
	tx, err := s.db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	return authorizeDependencies(t.Context(), tx, p, ids)
}

func TestDependenciesFailureCascadeIsDurableAndNeverExecutes(t *testing.T) {
	for _, failure := range []string{"cancelled", "failed"} {
		t.Run(failure, func(t *testing.T) { testDependenciesFailureCascade(t, failure) })
	}
}

func testDependenciesFailureCascade(t *testing.T, failure string) {
	s, from, backend, _ := fixture(t)
	worker := dependencyWorker(t, s)
	a := sendDependency(t, s, from, "backend", "a")
	b := sendDependency(t, s, from, "worker", "b", a.ID)
	c := sendDependency(t, s, from, "backend", "c", b.ID)
	actor := from
	if failure == "failed" {
		var err error
		a, err = s.Claim(t.Context(), backend.ID)
		require.NoError(t, err)
		actor = backend
	}
	_, err := s.Transition(t.Context(), actor.ID, a.ID, a.Revision, failure, json.RawMessage(`{}`))
	require.NoError(t, err)
	_, err = s.Claim(t.Context(), worker.ID)
	require.ErrorIs(t, err, ErrNotFound)
	restarted, err := NewStore(t.Context(), s.db)
	require.NoError(t, err)
	_, err = restarted.Claim(t.Context(), backend.ID)
	require.ErrorIs(t, err, ErrNotFound)
	for _, id := range []string{b.ID, c.ID} {
		m, err := restarted.Read(t.Context(), from.ID, id)
		require.NoError(t, err)
		require.Equal(t, Failed, m.State)
		require.Zero(t, m.ExecutionAttempts)
	}
	events, err := s.Events(t.Context(), from.ID, 0, 100)
	require.NoError(t, err)
	failures := 0
	for _, e := range events {
		if e.Type == "failed" && e.MessageID != a.ID {
			require.Contains(t, string(e.Payload), "dependency_failed")
			failures++
		}
	}
	require.Equal(t, 2, failures)
	escalations, err := s.ReadEscalations(t.Context(), from.ID, 0, 100)
	require.NoError(t, err)
	require.Len(t, escalations, 2)
	// Repeated polling produces no duplicate failures/escalations or retries.
	_, err = s.Claim(t.Context(), worker.ID)
	require.ErrorIs(t, err, ErrNotFound)
	escalations, err = s.ReadEscalations(t.Context(), from.ID, 0, 100)
	require.NoError(t, err)
	require.Len(t, escalations, 2)
}

func TestDependenciesDeadlineAndLeaseFencing(t *testing.T) {
	s, from, backend, _ := fixture(t)
	dependencyWorker(t, s)
	prereq := sendDependency(t, s, from, "worker", "pending")
	in := request("deadline")
	in.DependsOn = []string{prereq.ID}
	in.Deadline = time.Now().Add(time.Hour)
	waiting, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	_, err = s.db.ExecContext(t.Context(), `UPDATE coordination_messages SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1`, waiting.ID)
	require.NoError(t, err)
	lease, err := s.AcquireLease(t.Context(), backend.ID, uuid.NewString(), time.Second)
	require.NoError(t, err)
	wrong := lease
	wrong.OwnerID = uuid.NewString()
	_, err = s.ClaimLease(t.Context(), wrong)
	require.ErrorIs(t, err, ErrLeaseLost)
	m, err := s.Read(t.Context(), from.ID, waiting.ID)
	require.NoError(t, err)
	require.Equal(t, Accepted, m.State)
	_, err = s.ClaimLease(t.Context(), lease)
	require.ErrorIs(t, err, ErrNotFound)
	m, err = s.Read(t.Context(), from.ID, waiting.ID)
	require.NoError(t, err)
	require.Equal(t, Failed, m.State)
	require.Zero(t, m.ExecutionAttempts)
	events, err := s.Events(t.Context(), from.ID, 0, 100)
	require.NoError(t, err)
	require.Contains(t, string(events[len(events)-1].Payload), "deadline_elapsed_before_start")
}

func TestDependenciesPreserveLegacyReceiptHashes(t *testing.T) {
	s, from, _, _ := fixture(t)
	in := request("legacy-hash")
	// Historical SendInput's field order and tags are the wire format used by old hashes.
	legacy := struct {
		To             string          `json:"to"`
		Kind           string          `json:"kind"`
		ConversationID string          `json:"conversation_id,omitempty"`
		TaskID         string          `json:"task_id,omitempty"`
		InReplyTo      string          `json:"in_reply_to,omitempty"`
		Payload        json.RawMessage `json:"payload"`
		Deadline       time.Time       `json:"deadline,omitempty"`
		IdempotencyKey string          `json:"idempotency_key"`
	}{To: in.To, Kind: in.Kind, Payload: in.Payload, IdempotencyKey: in.IdempotencyKey}
	data, err := json.Marshal(legacy)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	m, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	var got []byte
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT request_hash FROM coordination_messages WHERE id=$1`, m.ID).Scan(&got))
	require.Equal(t, digest[:], got)
	in.DependsOn = []string{}
	replayed, err := s.Send(t.Context(), from.ID, in)
	require.NoError(t, err)
	require.Equal(t, m.ID, replayed.ID)
	require.False(t, errors.Is(err, ErrIdempotency))
}
