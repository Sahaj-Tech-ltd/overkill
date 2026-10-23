package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestPeerCLIConfigurationPrivateCredentialAtomicAndScoped(t *testing.T) {
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	folder := t.TempDir()
	front := session.NewSession(folder)
	back := session.NewSession(folder)
	outside := session.NewSession(t.TempDir())
	for _, s := range []*session.Session{front, back, outside} {
		require.NoError(t, sessions.Create(t.Context(), s))
	}
	_, err = store.RegisterSessionPeer(t.Context(), front.ID, "frontend", "UI")
	require.NoError(t, err)
	backend, err := store.RegisterSessionPeer(t.Context(), back.ID, "backend", "API")
	require.NoError(t, err)
	_, err = store.RegisterSessionPeer(t.Context(), outside.ID, "outside", "isolated")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "peer-secret")
	peer, err := configurePeerCredential(t.Context(), store, front.ID, back.ID, "http://127.0.0.1:8421", []string{"users-schema"}, path)
	require.NoError(t, err)
	require.Equal(t, backend.ID, peer.ID)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	token, err := os.ReadFile(path)
	require.NoError(t, err)
	actor, err := store.Authenticate(t.Context(), string(token))
	require.NoError(t, err)
	require.Equal(t, backend.ID, actor.ID)
	require.Equal(t, peer.Endpoint, actor.Endpoint)
	require.Equal(t, []string{"users-schema"}, actor.Capabilities)
	_, err = configurePeerCredential(t.Context(), store, front.ID, back.ID, "http://127.0.0.1:9999", nil, path)
	require.Error(t, err)
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, token, unchanged)
	current, err := store.PeerForSession(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, peer.Endpoint, current.Endpoint)
	rejected := filepath.Join(t.TempDir(), "must-not-exist")
	_, err = configurePeerCredential(t.Context(), store, front.ID, outside.ID, "http://127.0.0.1:8421", nil, rejected)
	require.ErrorIs(t, err, coordination.ErrUnauthorized)
	_, err = os.Stat(rejected)
	require.True(t, os.IsNotExist(err))
	var unknown string
	_, err = store.ConfigureSessionPeer(t.Context(), front.ID, back.ID, "http://127.0.0.1:8888", nil, func(token string) error { unknown = token; return errors.New("fixture disk failure") })
	require.Error(t, err)
	_, err = store.Authenticate(context.Background(), unknown)
	require.ErrorIs(t, err, coordination.ErrUnauthorized)
	current, err = store.PeerForSession(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, peer.Endpoint, current.Endpoint)
	// A commit error is reported conservatively and retains the private file,
	// rather than losing a potentially committed credential on ambiguous I/O.
	_, err = db.ExecContext(t.Context(), `CREATE FUNCTION reject_peer_credential_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture deferred rejection'; END $$;CREATE CONSTRAINT TRIGGER reject_peer_credential_commit AFTER INSERT ON coordination_credentials DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_peer_credential_commit()`)
	require.NoError(t, err)
	uncertainPath := filepath.Join(t.TempDir(), "retained-secret")
	_, err = configurePeerCredential(t.Context(), store, front.ID, back.ID, "http://127.0.0.1:7777", nil, uncertainPath)
	require.ErrorIs(t, err, coordination.ErrPeerCredentialUnconfirmed)
	retained, err := os.ReadFile(uncertainPath)
	require.NoError(t, err)
	require.Len(t, retained, 64)
	_, err = store.Authenticate(t.Context(), string(retained))
	require.ErrorIs(t, err, coordination.ErrUnauthorized)
	current, err = store.PeerForSession(t.Context(), back.ID)
	require.NoError(t, err)
	require.Equal(t, peer.Endpoint, current.Endpoint)
	// A session moved to another workspace cannot provision the earlier binding.
	_, err = db.ExecContext(t.Context(), `UPDATE sessions SET folder=$2 WHERE id=$1`, back.ID, t.TempDir())
	require.NoError(t, err)
	_, err = configurePeerCredential(t.Context(), store, front.ID, back.ID, "http://127.0.0.1:8421", nil, rejected)
	require.ErrorIs(t, err, coordination.ErrUnauthorized)
}
