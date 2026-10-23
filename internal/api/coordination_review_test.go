package api

import (
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCoordinationReviewApplicationAuthorityAndExplicitBudget(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	coord, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	folder := t.TempDir()
	front, back := session.NewSession(folder), session.NewSession(folder)
	require.NoError(t, sessions.Create(t.Context(), front))
	require.NoError(t, sessions.Create(t.Context(), back))
	f, err := coord.RegisterSessionPeer(t.Context(), front.ID, "frontend", "UI")
	require.NoError(t, err)
	b, err := coord.RegisterSessionPeer(t.Context(), back.ID, "backend", "API")
	require.NoError(t, err)
	message, err := coord.SendWithBudget(t.Context(), f.ID, coordination.SendInput{To: b.Name, Kind: "request", Payload: json.RawMessage(`"Build endpoint"`), IdempotencyKey: "review-api"}, coordination.UsageLimits{Tokens: 20})
	require.NoError(t, err)
	message, err = coord.Claim(t.Context(), b.ID)
	require.NoError(t, err)
	message, err = coord.Transition(t.Context(), b.ID, message.ID, message.Revision, "blocked", json.RawMessage(`{"reason":"review"}`))
	require.NoError(t, err)
	usage, err := coord.UsageForMessage(t.Context(), f.ID, message.ID)
	require.NoError(t, err)
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, CoordStore: coord})
	defer server.Close()
	input := map[string]any{"message_id": message.ID, "expected_revision": message.Revision, "expected_usage_revision": usage.Revision, "review_id": uuid.NewString(), "note": "Reviewed existing effects", "limits": map[string]any{"tokens": 100, "cost_usd": 0}, "requeue": true}
	call := func(sid string, in any, extra map[string]any) (any, *RPCError) {
		t.Helper()
		params := map[string]any{"session_id": sid, "input": in}
		for k, v := range extra {
			params[k] = v
		}
		data, err := json.Marshal(params)
		require.NoError(t, err)
		return server.handleCoordination(t.Context(), "coordination.review", data)
	}
	for _, limits := range []any{nil, map[string]any{}, map[string]any{"tokens": 100}, map[string]any{"tokens": nil, "cost_usd": 0}, map[string]any{"tokens": 100, "cost_usd": nil}} {
		bad := map[string]any{}
		for k, v := range input {
			bad[k] = v
		}
		bad["limits"] = limits
		_, rpcErr := call(front.ID, bad, nil)
		require.NotNil(t, rpcErr)
	}
	_, rpcErr := call(front.ID, input, map[string]any{"owner_peer_id": f.ID})
	require.NotNil(t, rpcErr)
	_, rpcErr = call(back.ID, input, nil)
	require.NotNil(t, rpcErr)
	result, rpcErr := call(front.ID, input, nil)
	require.Nil(t, rpcErr)
	receipt := result.(coordination.ReviewRequestResult)
	require.Equal(t, coordination.Accepted, receipt.Message.State)
	require.EqualValues(t, 100, receipt.Usage.Limits.Tokens)
	again, rpcErr := call(front.ID, input, nil)
	require.Nil(t, rpcErr)
	a, _ := json.Marshal(result)
	z, _ := json.Marshal(again)
	require.JSONEq(t, string(a), string(z))
}
