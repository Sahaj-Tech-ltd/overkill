package coordination

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCallbackDisabledPeerCannotAcknowledgeOrStartHTTP(t *testing.T) {
	store, peer, _, _ := fixture(t)
	sub, _ := callbackSub(t, store, peer)
	_, err := store.Send(t.Context(), peer.ID, request("disabled-ack"))
	require.NoError(t, err)
	claim, err := store.claimCallback(t.Context(), "revocation-test")
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.NoError(t, store.DisablePeer(t.Context(), peer.ID))
	owned, err := store.callbackOwned(t.Context(), claim)
	require.NoError(t, err)
	require.False(t, owned)
	require.ErrorIs(t, store.finishCallback(t.Context(), claim, 204, "", false, false), ErrLeaseLost)
	var cursor int64
	var state string
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT cursor FROM coordination_callback_subscriptions WHERE id=$1`, sub.ID).Scan(&cursor))
	require.NoError(t, store.db.QueryRowContext(t.Context(), `SELECT state FROM coordination_callback_deliveries WHERE subscription_id=$1`, sub.ID).Scan(&state))
	require.Zero(t, cursor)
	require.Equal(t, "pending", state)
	client := &http.Client{Transport: callbackRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Error("disabled peer must not start HTTP")
		return callbackResponse(204), nil
	})}
	worker := &CallbackWorker{Store: store, Destinations: map[string]string{"updates": "https://callback.example"}}
	status, failure, _ := worker.attempt(t.Context(), client, claim, time.Second)
	require.Zero(t, status)
	require.Equal(t, "ownership_lost", failure)
}
