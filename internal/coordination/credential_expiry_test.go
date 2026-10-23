package coordination

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

func expiryFixture(t *testing.T) (*Store, Peer, Peer, string, string, string) {
	t.Helper()
	store, front, back, legacy := fixture(t)
	sessions, err := session.NewPostgresStore(store.db)
	require.NoError(t, err)
	ids := []string{}
	for _, peer := range []Peer{front, back} {
		s := session.NewSession(peer.Workspace)
		require.NoError(t, sessions.Create(t.Context(), s))
		require.NoError(t, store.BindSession(t.Context(), peer.ID, s.ID))
		ids = append(ids, s.ID)
	}
	return store, front, back, ids[0], ids[1], legacy
}
func TestPeerCredentialExpiryMigrationPreservesLegacyAuthentication(t *testing.T) {
	store, front, _, _, _, legacy := expiryFixture(t)
	hash := sha256.Sum256([]byte(legacy))
	_, err := store.db.ExecContext(t.Context(), `ALTER TABLE coordination_credentials DROP COLUMN expires_at`)
	require.NoError(t, err)
	migrated, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	actor, err := migrated.Authenticate(t.Context(), legacy)
	require.NoError(t, err)
	require.Equal(t, front.ID, actor.ID)
	var expires sql.NullTime
	var storedHash []byte
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT expires_at,token_hash FROM coordination_credentials WHERE token_hash=$1`, hash[:]).Scan(&expires, &storedHash))
	require.False(t, expires.Valid)
	require.Equal(t, hash[:], storedHash)
	_, err = NewStore(t.Context(), store.db)
	require.NoError(t, err)
	_, err = migrated.Authenticate(t.Context(), legacy)
	require.NoError(t, err)
}
func TestPeerCredentialTTLBoundariesExpiryRestartAndRotation(t *testing.T) {
	store, front, back, frontSession, backSession, legacy := expiryFixture(t)
	for _, ttl := range []time.Duration{0, -time.Minute, time.Second, MaxPeerCredentialTTL + time.Second} {
		called := false
		_, _, err := store.ConfigureSessionPeerWithTTL(t.Context(), frontSession, backSession, "http://127.0.0.1:8421", nil, ttl, func(string) error { called = true; return nil })
		require.Error(t, err)
		require.False(t, called)
	}
	var token string
	_, metadata, err := store.ConfigureSessionPeerWithTTL(t.Context(), frontSession, backSession, "http://127.0.0.1:8421", nil, MinPeerCredentialTTL, func(value string) error { token = value; return nil })
	require.NoError(t, err)
	require.NotNil(t, metadata.ExpiresAt)
	hash := sha256.Sum256([]byte(token))
	var remaining float64
	var expires time.Time
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT expires_at,extract(epoch FROM expires_at-clock_timestamp()) FROM coordination_credentials WHERE token_hash=$1`, hash[:]).Scan(&expires, &remaining))
	require.True(t, expires.Equal(*metadata.ExpiresAt))
	require.Greater(t, remaining, 55.0)
	require.LessOrEqual(t, remaining, 60.0)
	peer, scoped, err := store.AuthenticateScoped(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, back.ID, peer.ID)
	message, err := store.Send(scoped, back.ID, SendInput{To: front.Name, Kind: "request", Payload: []byte(`{}`), IdempotencyKey: "before-expiry"})
	require.NoError(t, err)
	_, err = store.db.ExecContext(t.Context(), `UPDATE coordination_credentials SET expires_at=clock_timestamp()+interval '100 milliseconds' WHERE token_hash=$1`, hash[:])
	require.NoError(t, err)
	waiting, cancel := context.WithTimeout(scoped, 2*time.Second)
	defer cancel()
	_, err = store.Await(waiting, back.ID, message.ID, message.Revision)
	require.ErrorIs(t, err, ErrUnauthorized)
	_, err = store.Authenticate(t.Context(), token)
	require.ErrorIs(t, err, ErrUnauthorized)
	_, err = store.Send(scoped, back.ID, SendInput{To: front.Name, Kind: "progress", Payload: []byte(`{}`), IdempotencyKey: "expired-cached-auth"})
	require.ErrorIs(t, err, ErrUnauthorized)
	fresh, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	_, err = fresh.Authenticate(t.Context(), token)
	require.ErrorIs(t, err, ErrUnauthorized)
	_, err = fresh.Authenticate(t.Context(), legacy)
	require.NoError(t, err, "migration/expiry does not silently retire legacy credentials")
	var revoked bool
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT revoked FROM coordination_credentials WHERE token_hash=$1`, hash[:]).Scan(&revoked))
	require.False(t, revoked, "expiry is not revocation")
	var replacement string
	_, metadata, err = fresh.ConfigureSessionPeerWithTTL(t.Context(), frontSession, backSession, "http://127.0.0.1:8421", nil, MaxPeerCredentialTTL, func(value string) error { replacement = value; return nil })
	require.NoError(t, err)
	require.NotNil(t, metadata.ExpiresAt)
	_, err = fresh.Authenticate(t.Context(), replacement)
	require.NoError(t, err)
	require.NoError(t, fresh.RevokePeerCredential(t.Context(), frontSession, backSession, token))
	require.NoError(t, fresh.RevokePeerCredential(t.Context(), frontSession, backSession, token))
	_, err = fresh.Authenticate(t.Context(), replacement)
	require.NoError(t, err, "retiring old key leaves replacement and peer enabled")
	_, replacementScope, err := fresh.AuthenticateScoped(t.Context(), replacement)
	require.NoError(t, err)
	retried, err := fresh.Send(replacementScope, back.ID, SendInput{To: front.Name, Kind: "request", Payload: []byte(`{}`), IdempotencyKey: "before-expiry"})
	require.NoError(t, err)
	require.Equal(t, message.ID, retried.ID, "rotation preserves original idempotency receipt")
	require.NoError(t, fresh.DisablePeer(t.Context(), back.ID))
	_, err = fresh.Authenticate(t.Context(), replacement)
	require.ErrorIs(t, err, ErrUnauthorized)
	var count int
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_messages WHERE from_peer=$1`, back.ID).Scan(&count))
	require.Equal(t, 1, count, "expired credential created no second receipt")
}

func TestPeerCredentialRevocationCannotRetireForeignProjectKey(t *testing.T) {
	store, _, back, frontSession, backSession, _ := expiryFixture(t)
	var token string
	_, _, err := store.ConfigureSessionPeerWithTTL(t.Context(), frontSession, backSession, "http://127.0.0.1:8421", nil, time.Hour, func(value string) error { token = value; return nil })
	require.NoError(t, err)
	sessions, err := session.NewPostgresStore(store.db)
	require.NoError(t, err)
	outside := session.NewSession(t.TempDir())
	require.NoError(t, sessions.Create(t.Context(), outside))
	peer, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: "outside", Name: "outsider", Role: "isolated", Workspace: outside.Folder})
	require.NoError(t, err)
	require.NoError(t, store.BindSession(t.Context(), peer.ID, outside.ID))
	require.ErrorIs(t, store.RevokePeerCredential(t.Context(), outside.ID, backSession, token), ErrUnauthorized)
	require.ErrorIs(t, store.RevokePeerCredential(t.Context(), frontSession, frontSession, token), ErrNotFound)
	actor, err := store.Authenticate(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, back.ID, actor.ID)
}
