package acp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func expiryIngressFixture(t *testing.T) (*sql.DB, *coordination.Store, []coordination.Peer, []string, string, string) {
	t.Helper()
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	var peers []coordination.Peer
	var ids []string
	for _, name := range []string{"frontend", "backend"} {
		peer, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "expiry", Name: name, Role: name, Workspace: t.TempDir()})
		require.NoError(t, err)
		s := session.NewSession(peer.Workspace)
		require.NoError(t, sessions.Create(t.Context(), s))
		require.NoError(t, store.BindSession(t.Context(), peer.ID, s.ID))
		peers = append(peers, peer)
		ids = append(ids, s.ID)
	}
	var token string
	_, _, err = store.ConfigureSessionPeerWithTTL(t.Context(), ids[0], ids[1], "http://127.0.0.1:8421", nil, time.Minute, func(value string) error { token = value; return nil })
	require.NoError(t, err)
	server := NewServer(Config{Token: "not-peer", Coordination: store})
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(func() { httpServer.Close(); _ = server.Shutdown(context.Background()) })
	return db, store, peers, ids, token, httpServer.URL
}
func TestPeerCredentialExpiryRollsBackRequestAdmittedBeforeExpiry(t *testing.T) {
	db, _, peers, _, token, endpoint := expiryIngressFixture(t)
	var key int64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT hashtextextended(current_schema()||':expiry-fixture',0)`).Scan(&key))
	_, err := db.ExecContext(t.Context(), `CREATE FUNCTION block_expiring_peer_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(hashtextextended(current_schema()||':expiry-fixture',0)); RETURN NEW; END $$;CREATE TRIGGER block_expiring_peer_insert BEFORE INSERT ON coordination_messages FOR EACH ROW EXECUTE FUNCTION block_expiring_peer_insert()`)
	require.NoError(t, err)
	holder, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer holder.Rollback()
	_, err = holder.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock($1)`, key)
	require.NoError(t, err)
	hash := sha256.Sum256([]byte(token))
	_, err = db.ExecContext(t.Context(), `UPDATE coordination_credentials SET expires_at=clock_timestamp()+interval '2 seconds' WHERE token_hash=$1`, hash[:])
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() {
		_, err := NewClient(endpoint, token).Coordination(t.Context(), "send", CoordinationRequest{Send: &coordination.SendInput{To: peers[0].Name, Kind: "request", Payload: json.RawMessage(`{}`), IdempotencyKey: "wait-through-expiry"}})
		result <- err
	}()
	hi, lo := int64(uint64(key)>>32), int64(uint64(key)&0xffffffff)
	require.Eventually(t, func() bool {
		var waiting bool
		err := db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted AND classid::bigint=$1 AND objid::bigint=$2)`, hi, lo).Scan(&waiting)
		return err == nil && waiting
	}, time.Second, 5*time.Millisecond, "authenticated mutation must reach the held insert gate")
	require.Eventually(t, func() bool {
		var expired bool
		err := db.QueryRowContext(t.Context(), `SELECT clock_timestamp()>=expires_at FROM coordination_credentials WHERE token_hash=$1`, hash[:]).Scan(&expired)
		return err == nil && expired
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, holder.Rollback())
	require.ErrorContains(t, <-result, "401")
	for _, table := range []string{"coordination_messages", "coordination_events", "coordination_task_usage"} {
		var count int
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count, "expired transaction cannot commit "+table)
	}
}

func TestExpiredPeerCredentialCannotReuseLiveExecutionCapability(t *testing.T) {
	db, store, peers, ids, token, endpoint := expiryIngressFixture(t)
	root, err := store.Send(t.Context(), peers[0].ID, coordination.SendInput{To: peers[1].Name, Kind: "request", Payload: json.RawMessage(`{}`), IdempotencyKey: "active"})
	require.NoError(t, err)
	proofReady := make(chan *coordination.TransportExecution, 1)
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	executor := transportProofExecutor{peer: peers[1].ID, run: func(ctx context.Context, m coordination.Message) (json.RawMessage, error) {
		proofReady <- coordination.ExecutionTransportProof(ctx)
		select {
		case <-release:
			return json.RawMessage(`{"done":true}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	go func() {
		done <- (&coordination.Dispatcher{Store: store, Executor: executor, Options: coordination.DispatchOptions{PollInterval: 10 * time.Millisecond, LeaseTTL: time.Second}}).Run(ctx)
	}()
	defer func() { cancel(); require.NoError(t, <-done) }()
	var proof *coordination.TransportExecution
	select {
	case proof = <-proofReady:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not claim")
	}
	require.NotNil(t, proof)
	input := CoordinationRequest{Execution: proof, Send: &coordination.SendInput{To: peers[0].Name, Kind: "progress", InReplyTo: root.ID, Payload: json.RawMessage(`{"percent":10}`), IdempotencyKey: "before-expiry"}}
	_, err = NewClient(endpoint, token).Coordination(t.Context(), "send", input)
	require.NoError(t, err)
	hash := sha256.Sum256([]byte(token))
	_, err = db.ExecContext(t.Context(), `UPDATE coordination_credentials SET expires_at=clock_timestamp()-interval '1 second' WHERE token_hash=$1`, hash[:])
	require.NoError(t, err)
	input.Send.IdempotencyKey = "expired-owned-proof"
	_, err = NewClient(endpoint, token).Coordination(t.Context(), "send", input)
	require.ErrorContains(t, err, "401")
	for _, operation := range []string{"peers", "inbox", "status", "await", "claim", "transition", "reply", "cancel", "recover"} {
		_, err := NewClient(endpoint, token).Coordination(t.Context(), operation, CoordinationRequest{MessageID: root.ID, ExpectedRevision: root.Revision, Payload: json.RawMessage(`{}`)})
		require.ErrorContains(t, err, "401", operation)
	}
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_messages WHERE idempotency_key='expired-owned-proof'`).Scan(&count))
	require.Zero(t, count)
	// Rotation creates another key for the same principal, not another lease.
	var replacement string
	_, _, err = store.ConfigureSessionPeerWithTTL(t.Context(), ids[0], ids[1], "http://127.0.0.1:8421", nil, time.Hour, func(value string) error { replacement = value; return nil })
	require.NoError(t, err)
	input.Send.IdempotencyKey = "replacement-owned-proof"
	_, err = NewClient(endpoint, replacement).Coordination(t.Context(), "send", input)
	require.NoError(t, err)
	fresh, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	_, err = fresh.Authenticate(t.Context(), token)
	require.ErrorIs(t, err, coordination.ErrUnauthorized)
	_, err = fresh.Authenticate(t.Context(), replacement)
	require.NoError(t, err)
	restarted := NewServer(Config{Token: "not-peer", Coordination: fresh})
	defer restarted.Shutdown(context.Background())
	freshHTTP := httptest.NewServer(restarted.Handler())
	defer freshHTTP.Close()
	_, err = NewClient(freshHTTP.URL, token).Coordination(t.Context(), "peers", CoordinationRequest{})
	require.ErrorContains(t, err, "401")
	_, err = NewClient(freshHTTP.URL, replacement).Coordination(t.Context(), "peers", CoordinationRequest{})
	require.NoError(t, err)

	close(release)
	require.Eventually(t, func() bool {
		m, err := store.Read(t.Context(), peers[0].ID, root.ID)
		return err == nil && m.State == coordination.Completed
	}, 3*time.Second, 10*time.Millisecond, "expiry does not undo already admitted trusted worker execution")
}
