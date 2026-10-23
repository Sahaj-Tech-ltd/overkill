package coordination

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRuntimePresenceSnapshotsExpiryAndWorkspaceBoundary(t *testing.T) {
	s, _, _, _ := fixture(t)
	ctx := t.Context()
	sessions := localSessions(t, s, t.TempDir(), 2)
	front, err := s.RegisterSessionPeer(ctx, sessions[0].ID, "front", "UI")
	require.NoError(t, err)
	back, err := s.RegisterSessionPeer(ctx, sessions[1].ID, "back", "API")
	require.NoError(t, err)
	observer, other := uuid.NewString(), uuid.NewString()
	list, err := s.LocalPeersForSession(ctx, sessions[0].ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	for _, p := range list {
		require.Equal(t, "unknown", p.Presence.State)
	}
	require.NoError(t, s.PublishRuntimePresence(ctx, observer, []RuntimeObservation{{sessions[0].ID, "working"}}))
	read := func(id string) RuntimePresence {
		t.Helper()
		p, err := s.RuntimePresenceForPeers(ctx, []string{id})
		require.NoError(t, err)
		return p[id]
	}
	p := read(front.ID)
	require.Equal(t, "working", p.State)
	require.Equal(t, 1, p.Observers)
	require.Equal(t, "runtime_lease", p.Source)
	require.WithinDuration(t, time.Now().Add(RuntimePresenceTTL), *p.ExpiresAt, 2*time.Second)
	require.Equal(t, "unknown", read(back.ID).State)
	require.NoError(t, s.PublishRuntimePresence(ctx, other, []RuntimeObservation{{sessions[0].ID, "ready"}}))
	require.Equal(t, "multiple_runtimes", read(front.ID).State)
	require.Equal(t, 2, read(front.ID).Observers)
	require.NoError(t, s.RemoveRuntimeObserver(ctx, other))
	require.Equal(t, "working", read(front.ID).State)
	// A replacement snapshot removes a discarded runtime without affecting peers.
	require.NoError(t, s.PublishRuntimePresence(ctx, observer, []RuntimeObservation{{sessions[1].ID, "ready"}}))
	require.Equal(t, "unknown", read(front.ID).State)
	require.Equal(t, "ready", read(back.ID).State)
	// Simulate a crashed observer's clock-expired row, without waiting twenty seconds.
	_, err = s.db.ExecContext(ctx, `UPDATE coordination_runtime_presence SET expires_at=clock_timestamp()-interval '1 second' WHERE observer_id=$1`, observer)
	require.NoError(t, err)
	require.Equal(t, "unknown", read(back.ID).State)
	foreign := localSessions(t, s, t.TempDir(), 1)[0]
	isolated, err := s.LocalPeersForSession(ctx, foreign.ID)
	require.NoError(t, err)
	require.Empty(t, isolated)
}

func TestRuntimePresenceRejectsForgeryAndRollsBackFailedSnapshot(t *testing.T) {
	s, _, _, _ := fixture(t)
	ctx := t.Context()
	metadata := localSessions(t, s, t.TempDir(), 1)[0]
	peer, err := s.RegisterSessionPeer(ctx, metadata.ID, "local", "UI")
	require.NoError(t, err)
	observer := uuid.NewString()
	require.NoError(t, s.PublishRuntimePresence(ctx, observer, []RuntimeObservation{{metadata.ID, "ready"}}))
	for _, items := range [][]RuntimeObservation{{{metadata.ID, "online"}}, {{"invalid-session", "ready"}}, {{metadata.ID, "ready"}, {metadata.ID, "working"}}} {
		require.Error(t, s.PublishRuntimePresence(ctx, observer, items))
	}
	require.Error(t, s.PublishRuntimePresence(ctx, "forged", nil))
	_, err = s.db.ExecContext(ctx, `CREATE FUNCTION reject_presence() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='working' THEN RAISE EXCEPTION 'injected failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_presence BEFORE INSERT ON coordination_runtime_presence FOR EACH ROW EXECUTE FUNCTION reject_presence()`)
	require.NoError(t, err)
	require.Error(t, s.PublishRuntimePresence(ctx, observer, []RuntimeObservation{{metadata.ID, "working"}}))
	p, err := s.RuntimePresenceForPeers(ctx, []string{peer.ID})
	require.NoError(t, err)
	require.Equal(t, "ready", p[peer.ID].State, "failed replacement must retain previous committed observation")
	_, err = s.db.ExecContext(ctx, `UPDATE coordination_peers SET enabled=false WHERE id=$1`, peer.ID)
	require.NoError(t, err)
	require.NoError(t, s.PublishRuntimePresence(ctx, observer, []RuntimeObservation{{metadata.ID, "ready"}}))
	p, err = s.RuntimePresenceForPeers(ctx, []string{peer.ID})
	require.NoError(t, err)
	require.Equal(t, "unknown", p[peer.ID].State)
}
