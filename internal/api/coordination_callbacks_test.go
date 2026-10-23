package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

type apiCallbackTransport func(*http.Request) (*http.Response, error)

func (f apiCallbackTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCallbackAuthenticatedApplicationRPCAndOwnedDelivery(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	var metadata []*session.Session
	for range 3 {
		item := session.NewSession(t.TempDir())
		require.NoError(t, sessions.Create(t.Context(), item))
		metadata = append(metadata, item)
	}
	// Front and back share a canonical workspace; the third session is foreign.
	metadata[1].Folder = metadata[0].Folder
	require.NoError(t, sessions.Save(t.Context(), metadata[1]))
	var calls atomic.Int32
	started := make(chan struct{})
	ended := make(chan struct{})
	transport := apiCallbackTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		require.Empty(t, r.Header.Get("Authorization"))
		require.NotEmpty(t, r.Header.Get("X-Overkill-Signature"))
		close(started)
		<-r.Context().Done()
		close(ended)
		return nil, r.Context().Err()
	})
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, CoordStore: store, CallbackDestinations: map[string]string{"updates": "https://operator.example/events"}, CallbackTransport: transport})
	startHTTPServer(t, server)
	for index, name := range []string{"frontend", "backend", "foreign"} {
		response := sidebarRPC(t, server, "coordination.register", map[string]any{"session_id": metadata[index].ID, "name": name, "role": name})
		require.Nil(t, response.Error)
	}
	hold, err := server.beginSessionOperation(metadata[1].ID)
	require.NoError(t, err)
	defer hold()
	for _, params := range []any{map[string]any{"session_id": "missing", "destination": "updates"}, map[string]any{"session_id": metadata[0].ID, "destination": "unknown"}, map[string]any{"session_id": metadata[0].ID, "destination": "updates", "url": "https://attacker.example"}} {
		require.NotNil(t, sidebarRPC(t, server, "coordination.callback.subscribe", params).Error)
	}
	receipt := sidebarRPC(t, server, "coordination.callback.subscribe", map[string]any{"session_id": metadata[0].ID, "destination": "updates"})
	require.Nil(t, receipt.Error)
	encoded, err := json.Marshal(receipt.Result)
	require.NoError(t, err)
	var setup struct {
		Subscription coordination.CallbackSubscription `json:"subscription"`
		Secret       string                            `json:"signing_secret"`
	}
	require.NoError(t, json.Unmarshal(encoded, &setup))
	require.Len(t, setup.Secret, 64)
	require.NotEqual(t, server.AuthToken(), setup.Secret)
	rotated := sidebarRPC(t, server, "coordination.callback.rotate", map[string]any{"session_id": metadata[0].ID, "subscription_id": setup.Subscription.ID})
	require.Nil(t, rotated.Error)
	rotationData, err := json.Marshal(rotated.Result)
	require.NoError(t, err)
	var rotation struct {
		Secret string `json:"signing_secret"`
	}
	require.NoError(t, json.Unmarshal(rotationData, &rotation))
	require.Len(t, rotation.Secret, 64)
	require.NotEqual(t, setup.Secret, rotation.Secret)
	setup.Secret = rotation.Secret
	duplicate := sidebarRPC(t, server, "coordination.callback.subscribe", map[string]any{"session_id": metadata[0].ID, "destination": "updates"})
	require.NotNil(t, duplicate.Error)
	list := sidebarRPC(t, server, "coordination.callback.list", map[string]any{"session_id": metadata[0].ID})
	require.Nil(t, list.Error)
	encoded, err = json.Marshal(list.Result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), setup.Secret)
	for _, method := range []string{"coordination.callback.rotate", "coordination.callback.deliveries", "coordination.callback.cancel", "coordination.callback.retry"} {
		params := map[string]any{"session_id": metadata[2].ID, "subscription_id": setup.Subscription.ID}
		if method == "coordination.callback.retry" {
			params["sequence"] = 1
		}
		require.NotNil(t, sidebarRPC(t, server, method, params).Error)
	}
	sent := sidebarRPC(t, server, "coordination.send", map[string]any{"session_id": metadata[0].ID, "to": "backend", "kind": "request", "payload": "offline callback event", "idempotency_key": "callback-acceptance"})
	require.Nil(t, sent.Error)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("API-owned callback did not start")
	}
	cancelled := sidebarRPC(t, server, "coordination.callback.cancel", map[string]any{"session_id": metadata[0].ID, "subscription_id": setup.Subscription.ID})
	require.Nil(t, cancelled.Error)
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling subscription did not cancel HTTP attempt")
	}
	require.Eventually(t, func() bool {
		result := sidebarRPC(t, server, "coordination.callback.deliveries", map[string]any{"session_id": metadata[0].ID, "subscription_id": setup.Subscription.ID})
		if result.Error != nil {
			return false
		}
		data, _ := json.Marshal(result.Result)
		return strings.Contains(string(data), `"state":"cancelled"`)
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), calls.Load())
	again := sidebarRPC(t, server, "coordination.callback.subscribe", map[string]any{"session_id": metadata[0].ID, "destination": "updates", "after": 1000})
	require.Nil(t, again.Error)

	// Callback RPC methods use the existing bearer authentication boundary.
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.Addr()+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"coordination.callback.list","params":{}}`))
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
}
func TestCallbackConfigurationFailsStartupWithoutPartialAcceptance(t *testing.T) {
	for _, value := range []string{"broken", "null", `{"updates":"http://example.com"}`, `{"updates":"https://user:secret@example.com"}`} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("OVERKILL_COORDINATION_CALLBACK_DESTINATIONS", value)
			server := NewServer(ServerConfig{Config: config.Default()})
			for range 2 {
				require.Error(t, server.StartBackground(context.Background()))
				require.False(t, server.CoordinationStatus()["started"])
			}
		})
	}
}
