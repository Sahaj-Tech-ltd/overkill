package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) (*Store, Peer, Peer, string) {
	t.Helper()
	database, _ := testpg.Open(t)
	store, err := NewStore(t.Context(), database)
	require.NoError(t, err)
	frontend, credential, err := store.RegisterPeer(t.Context(), Peer{ProjectID: "project", Name: "frontend", Role: "build UI", Workspace: t.TempDir()})
	require.NoError(t, err)
	backend, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: "project", Name: "backend", Role: "build API", Workspace: t.TempDir()})
	require.NoError(t, err)
	return store, frontend, backend, credential
}

func request(key string) SendInput {
	return SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`{"question":"Is GET /users ready?"}`), IdempotencyKey: key}
}

func TestCredentialAndProjectBoundaries(t *testing.T) {
	store, frontend, backend, credential := fixture(t)
	ctx := t.Context()
	identified, err := store.Authenticate(ctx, credential)
	require.NoError(t, err)
	require.Equal(t, frontend.ID, identified.ID)
	_, err = store.Authenticate(ctx, strings.Repeat("0", 64))
	require.ErrorIs(t, err, ErrUnauthorized)
	other, _, err := store.RegisterPeer(ctx, Peer{ProjectID: "different", Name: "intruder", Role: "isolated", Workspace: t.TempDir()})
	require.NoError(t, err)
	_, err = store.Send(ctx, other.ID, request("unauthorized"))
	require.ErrorIs(t, err, ErrUnauthorized)
	m, err := store.Send(ctx, frontend.ID, request("accepted"))
	require.NoError(t, err)
	_, err = store.Read(ctx, other.ID, m.ID)
	require.ErrorIs(t, err, ErrNotFound)
	events, err := store.Events(ctx, other.ID, 0, 10)
	require.NoError(t, err)
	require.Empty(t, events)
	_, err = store.Transition(ctx, frontend.ID, m.ID, m.Revision, "completed", json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrUnauthorized)
	require.NoError(t, store.DisablePeer(ctx, frontend.ID))
	_, err = store.Authenticate(ctx, credential)
	require.ErrorIs(t, err, ErrUnauthorized)
	_, err = store.Read(ctx, frontend.ID, m.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.Read(ctx, backend.ID, m.ID)
	require.NoError(t, err, "recipient retains its own correspondence")
}

func TestConcurrentSendRetriesCreateOneReceiptAndEvent(t *testing.T) {
	store, frontend, _, _ := fixture(t)
	ctx := t.Context()
	const count = 24
	results := make(chan Message, count)
	failures := make(chan error, count)
	var workers sync.WaitGroup
	for range count {
		workers.Go(func() {
			m, err := store.Send(ctx, frontend.ID, request("same-command"))
			results <- m
			failures <- err
		})
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var id string
	for result := range results {
		if id == "" {
			id = result.ID
		}
		require.Equal(t, id, result.ID)
		require.Equal(t, Accepted, result.State)
	}
	events, err := store.Events(ctx, frontend.ID, 0, 100)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, int64(1), events[0].Sequence)
	different := request("same-command")
	different.Payload = json.RawMessage(`{"question":"different"}`)
	_, err = store.Send(ctx, frontend.ID, different)
	require.ErrorIs(t, err, ErrIdempotency)
}

func TestClaimsProgressRepliesAndReplaySurviveRestart(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	ctx := t.Context()
	m, err := store.Send(ctx, frontend.ID, request("workflow"))
	require.NoError(t, err)
	require.NotEmpty(t, m.ConversationID)
	claimed, err := store.Claim(ctx, backend.ID)
	require.NoError(t, err)
	require.Equal(t, m.ID, claimed.ID)
	require.Equal(t, Started, claimed.State)
	// Reconstructing service state never reclaims partially executed work.
	restarted, err := NewStore(ctx, store.db)
	require.NoError(t, err)
	_, err = restarted.Claim(ctx, backend.ID)
	require.ErrorIs(t, err, ErrBusy)
	inbox, err := restarted.Inbox(ctx, backend.ID, 10)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, Started, inbox[0].State)
	progress, err := restarted.Transition(ctx, backend.ID, claimed.ID, claimed.Revision, "progress", json.RawMessage(`{"percent":50}`))
	require.NoError(t, err)
	_, err = restarted.Transition(ctx, backend.ID, claimed.ID, claimed.Revision, "blocked", json.RawMessage(`{}`))
	require.ErrorIs(t, err, ErrConflict)
	done, err := restarted.Transition(ctx, backend.ID, m.ID, progress.Revision, "completed", json.RawMessage(`{"response_schema":{"users":"array"}}`))
	require.NoError(t, err)
	require.Equal(t, Completed, done.State)
	_, err = restarted.Transition(ctx, backend.ID, m.ID, done.Revision, "progress", json.RawMessage(`{}`))
	require.Error(t, err)
	reply, err := restarted.Send(ctx, backend.ID, SendInput{To: "frontend", Kind: "answer", InReplyTo: m.ID, Payload: json.RawMessage(`{"ready":true}`), IdempotencyKey: "answer-once"})
	require.NoError(t, err)
	require.Equal(t, m.ConversationID, reply.ConversationID)
	all, err := restarted.Events(ctx, frontend.ID, 0, 100)
	require.NoError(t, err)
	require.Len(t, all, 5)
	for i, event := range all {
		require.Equal(t, int64(i+1), event.Sequence)
	}
	replay, err := restarted.Events(ctx, frontend.ID, all[1].Sequence, 100)
	require.NoError(t, err)
	require.Equal(t, all[2:], replay)
}

func TestConcurrentWorkersNeverClaimOneMessageTwice(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	ctx := t.Context()
	_, err := store.Send(ctx, frontend.ID, request("one-task"))
	require.NoError(t, err)
	const count = 16
	results := make(chan error, count)
	var workers sync.WaitGroup
	for range count {
		workers.Go(func() { _, err := store.Claim(ctx, backend.ID); results <- err })
	}
	workers.Wait()
	close(results)
	claimed := 0
	for err := range results {
		if err == nil {
			claimed++
		} else {
			require.True(t, errors.Is(err, ErrNotFound) || errors.Is(err, ErrBusy), "%v", err)
		}
	}
	require.Equal(t, 1, claimed)
}

func TestSendRejectsInvalidAndCancelledRequests(t *testing.T) {
	store, frontend, _, _ := fixture(t)
	invalid := request("invalid")
	invalid.Payload = json.RawMessage(`invalid`)
	_, err := store.Send(t.Context(), frontend.ID, invalid)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = store.Send(ctx, frontend.ID, request("cancelled"))
	require.ErrorIs(t, err, context.Canceled)
	events, err := store.Events(t.Context(), frontend.ID, 0, 10)
	require.NoError(t, err)
	require.Empty(t, events)
}
