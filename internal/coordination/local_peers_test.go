package coordination

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

func localSessions(t *testing.T, store *Store, folder string, count int) []*session.Session {
	t.Helper()
	sessions, err := session.NewPostgresStore(store.db)
	require.NoError(t, err)
	result := make([]*session.Session, count)
	for i := range result {
		result[i] = session.NewSession(folder)
		require.NoError(t, sessions.Create(t.Context(), result[i]))
	}
	return result
}

func TestLocalPeerRegistrationIsAtomicIdempotentAndWorkspaceScoped(t *testing.T) {
	store, _, _, _ := fixture(t)
	sessions := localSessions(t, store, t.TempDir(), 3)
	var wg sync.WaitGroup
	peers := make(chan Peer, 16)
	failures := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			peer, err := store.RegisterSessionPeer(t.Context(), sessions[0].ID, "frontend", "Build the UI")
			peers <- peer
			failures <- err
		})
	}
	wg.Wait()
	close(peers)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	var front Peer
	for peer := range peers {
		if front.ID != "" {
			require.Equal(t, front.ID, peer.ID)
		}
		front = peer
	}
	_, err := store.RegisterSessionPeer(t.Context(), sessions[1].ID, "frontend", "Attempt transfer")
	require.ErrorIs(t, err, ErrConflict)
	_, err = store.PeerForSession(t.Context(), sessions[1].ID)
	require.ErrorIs(t, err, ErrNotFound)
	backend, err := store.RegisterSessionPeer(t.Context(), sessions[1].ID, "backend", "Build the API")
	require.NoError(t, err)
	list, err := store.LocalPeersForSession(t.Context(), sessions[2].ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, backend.ProjectID, front.ProjectID)
	other := localSessions(t, store, t.TempDir(), 1)[0]
	list, err = store.LocalPeersForSession(t.Context(), other.ID)
	require.NoError(t, err)
	require.Empty(t, list)
	var credentials int
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_credentials WHERE peer_id IN ($1,$2)`, front.ID, backend.ID).Scan(&credentials))
	require.Zero(t, credentials)
	_, err = store.db.ExecContext(t.Context(), `CREATE FUNCTION reject_local_binding() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture binding unavailable'; END $$; CREATE TRIGGER reject_local_binding BEFORE INSERT ON coordination_sessions FOR EACH ROW EXECUTE FUNCTION reject_local_binding()`)
	require.NoError(t, err)
	_, err = store.RegisterSessionPeer(t.Context(), sessions[2].ID, "reviewer", "Review changes")
	require.ErrorContains(t, err, "fixture binding unavailable")
	var orphan int
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_peers WHERE project_id=$1 AND name='reviewer'`, front.ProjectID).Scan(&orphan))
	require.Zero(t, orphan)
}

func TestCorrespondencePaginationIncludesTerminalMessagesWithoutLeaks(t *testing.T) {
	store, front, backend, _ := fixture(t)
	ids := make(map[string]bool)
	for i := range 5 {
		message, err := store.Send(t.Context(), front.ID, request(fmt.Sprintf("page-%d", i)))
		require.NoError(t, err)
		ids[message.ID] = true
	}
	first, err := store.Claim(t.Context(), backend.ID)
	require.NoError(t, err)
	_, err = store.Transition(t.Context(), backend.ID, first.ID, first.Revision, "completed", json.RawMessage(`{"done":true}`))
	require.NoError(t, err)
	before := ""
	sawCompleted := false
	for range 3 {
		page, err := store.Correspondence(t.Context(), front.ID, before, 2)
		require.NoError(t, err)
		for _, message := range page.Messages {
			require.True(t, ids[message.ID], "page repeated or leaked a message")
			delete(ids, message.ID)
			sawCompleted = sawCompleted || message.State == Completed
		}
		if !page.HasMore {
			break
		}
		require.NotEmpty(t, page.BeforeID)
		before = page.BeforeID
	}
	require.Empty(t, ids)
	require.True(t, sawCompleted)
	other, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: "other", Name: "isolated", Role: "Other", Workspace: t.TempDir()})
	require.NoError(t, err)
	_, err = store.Correspondence(t.Context(), other.ID, first.ID, 2)
	require.ErrorIs(t, err, ErrNotFound)
}
