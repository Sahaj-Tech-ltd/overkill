package coordination

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type callbackRoundTripper func(*http.Request) (*http.Response, error)

func (f callbackRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func callbackResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ignored provider diagnostic"))}
}
func runCallbacks(t *testing.T, w *CallbackWorker) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(4 * time.Second):
				t.Error("callback worker did not join cancellation")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}
func callbackSub(t *testing.T, s *Store, p Peer) (CallbackSubscription, string) {
	t.Helper()
	sub, key, err := s.SubscribeCallback(t.Context(), p.ID, "updates", 0)
	require.NoError(t, err)
	return sub, key
}

func TestCallbackSignedReplayPeerFilteringAndRestart(t *testing.T) {
	store, front, back, credential := fixture(t)
	sub, key := callbackSub(t, store, front)
	require.NotEqual(t, credential, key)
	other, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: "project", Name: "review", Role: "review", Workspace: t.TempDir()})
	require.NoError(t, err)
	message, err := store.Send(t.Context(), front.ID, request("visible"))
	require.NoError(t, err)
	message, err = store.Claim(t.Context(), back.ID)
	require.NoError(t, err)
	message, err = store.Transition(t.Context(), back.ID, message.ID, message.Revision, "completed", json.RawMessage(`{"result":"done offline"}`))
	require.NoError(t, err)
	_, err = store.Send(t.Context(), other.ID, request("private"))
	require.NoError(t, err)
	expected, err := store.Events(t.Context(), front.ID, 0, 100)
	require.NoError(t, err)
	require.Len(t, expected, 3)
	secret, err := hex.DecodeString(key)
	require.NoError(t, err)
	var mu sync.Mutex
	received := make(map[string][]byte)
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		id, seq, stamp := r.Header.Get("X-Overkill-Event-ID"), r.Header.Get("X-Overkill-Sequence"), r.Header.Get("X-Overkill-Timestamp")
		mac := hmac.New(sha256.New, secret)
		fmt.Fprintf(mac, "v1\n%s\n%s\n%s\n", id, seq, stamp)
		_, _ = mac.Write(body)
		if r.Header.Get("X-Overkill-Signature") != "v1="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("invalid wire signature")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("API credential leaked to callback")
		}
		var envelope struct {
			ID    string `json:"delivery_id"`
			Event Event  `json:"event"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Error(err)
		}
		if envelope.ID != id || envelope.Event.MessageID != message.ID {
			t.Error("cross-peer event or identity mismatch")
		}
		mu.Lock()
		received[id] = body
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	target, err := url.Parse(endpoint.URL)
	require.NoError(t, err)
	localTransport := endpoint.Client().Transport
	transport := callbackRoundTripper(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Host = target.Host
		clone.URL = &u
		return localTransport.RoundTrip(clone)
	})
	// Simulate application restart before any offline completion delivery.
	restarted, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	stop := runCallbacks(t, &CallbackWorker{Store: restarted, Destinations: map[string]string{"updates": "https://callbacks.example/events"}, Transport: transport, PollInterval: 10 * time.Millisecond})
	require.Eventually(t, func() bool {
		subs, e := store.CallbackSubscriptions(t.Context(), front.ID)
		return e == nil && len(subs) == 1 && subs[0].Cursor == expected[2].Sequence
	}, 3*time.Second, 10*time.Millisecond)
	stop()
	mu.Lock()
	require.Len(t, received, 3)
	mu.Unlock()
	deliveries, err := store.CallbackDeliveries(t.Context(), front.ID, sub.ID)
	require.NoError(t, err)
	require.Len(t, deliveries, 3)
	for _, delivery := range deliveries {
		require.Equal(t, "delivered", delivery.State)
		require.Equal(t, 1, delivery.Attempts)
	}
	encoded, err := json.Marshal(deliveries)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), key)
	// Callbacks only read event history; no second agent execution or revision.
	current, err := store.Read(t.Context(), front.ID, message.ID)
	require.NoError(t, err)
	require.Equal(t, message.Revision, current.Revision)
	require.NoError(t, store.callbackAccess(t.Context(), front.ID, sub.ID))
	_, err = store.CallbackDeliveries(t.Context(), other.ID, sub.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, store.CancelCallback(t.Context(), other.ID, sub.ID), ErrNotFound)
	require.ErrorIs(t, store.RetryCallback(t.Context(), other.ID, sub.ID, 1), ErrNotFound)
	_, _, err = store.SubscribeCallback(t.Context(), "missing", "updates", 0)
	require.ErrorIs(t, err, ErrUnauthorized)
	_, _, err = store.SubscribeCallback(t.Context(), front.ID, "updates", 0)
	require.ErrorIs(t, err, ErrConflict)
}

func TestCallbackAcceptedHTTPBeforeCrashUsesSameDeliveryIdentity(t *testing.T) {
	store, front, _, _ := fixture(t)
	sub, _ := callbackSub(t, store, front)
	_, err := store.Send(t.Context(), front.ID, request("crash"))
	require.NoError(t, err)
	first, err := store.claimCallback(t.Context(), "crashed-worker")
	require.NoError(t, err)
	require.NotNil(t, first)
	var ids []string
	var bodies []string
	worker := &CallbackWorker{Store: store, Destinations: map[string]string{"updates": "https://callback.example/events"}}
	client := &http.Client{Transport: callbackRoundTripper(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		ids = append(ids, r.Header.Get("X-Overkill-Event-ID"))
		bodies = append(bodies, string(data))
		return callbackResponse(204), nil
	})}
	status, _, _ := worker.attempt(t.Context(), client, first, time.Second)
	require.Equal(t, 204, status)
	// Simulate death after remote acceptance, before the local ack transaction.
	_, err = store.db.ExecContext(t.Context(), `UPDATE coordination_callback_deliveries SET lease_until=NOW()-INTERVAL '1 second' WHERE subscription_id=$1`, sub.ID)
	require.NoError(t, err)
	restarted, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	second, err := restarted.claimCallback(t.Context(), "restart-worker")
	require.NoError(t, err)
	require.NotNil(t, second)
	status, _, _ = worker.attempt(t.Context(), client, second, time.Second)
	require.Equal(t, 204, status)
	require.Equal(t, ids[0], ids[1])
	require.Equal(t, bodies[0], bodies[1])
	require.Equal(t, 2, second.attempts)
	require.NoError(t, restarted.finishCallback(t.Context(), second, 204, "", false, false))
	next, err := store.claimCallback(t.Context(), "again")
	require.NoError(t, err)
	require.Nil(t, next)
	require.ErrorIs(t, store.finishCallback(t.Context(), first, 204, "", false, false), ErrLeaseLost)
}

func TestCallbackBackoffDeadLetterAndExplicitRetry(t *testing.T) {
	store, front, _, _ := fixture(t)
	sub, _ := callbackSub(t, store, front)
	_, err := store.Send(t.Context(), front.ID, request("retry"))
	require.NoError(t, err)
	var fail atomic.Bool
	fail.Store(true)
	var calls atomic.Int32
	transport := callbackRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		if fail.Load() {
			return callbackResponse(503), nil
		}
		return callbackResponse(204), nil
	})
	stop := runCallbacks(t, &CallbackWorker{Store: store, Destinations: map[string]string{"updates": "https://callback.example"}, Transport: transport, PollInterval: 10 * time.Millisecond, MaxAttempts: 2})
	require.Eventually(t, func() bool {
		d, e := store.CallbackDeliveries(t.Context(), front.ID, sub.ID)
		return e == nil && len(d) == 1 && d[0].Attempts == 1 && d[0].Failure == "http_status" && d[0].NextAttempt.After(time.Now())
	}, time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		d, e := store.CallbackDeliveries(t.Context(), front.ID, sub.ID)
		return e == nil && len(d) == 1 && d[0].State == "dead"
	}, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, int32(2), calls.Load())
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(2), calls.Load())
	d, err := store.CallbackDeliveries(t.Context(), front.ID, sub.ID)
	require.NoError(t, err)
	fail.Store(false)
	require.NoError(t, store.RetryCallback(t.Context(), front.ID, sub.ID, d[0].Sequence))
	require.Eventually(t, func() bool {
		d, e := store.CallbackDeliveries(t.Context(), front.ID, sub.ID)
		return e == nil && len(d) == 1 && d[0].State == "delivered"
	}, time.Second, 10*time.Millisecond)
	stop()
	require.Equal(t, int32(3), calls.Load())
}

func TestCallbackCancellationAndShutdownOwnAttempt(t *testing.T) {
	for _, cancelSubscription := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelSubscription), func(t *testing.T) {
			store, front, _, _ := fixture(t)
			sub, _ := callbackSub(t, store, front)
			_, err := store.Send(t.Context(), front.ID, request("hold"))
			require.NoError(t, err)
			started, ended := make(chan struct{}), make(chan struct{})
			var once sync.Once
			transport := callbackRoundTripper(func(r *http.Request) (*http.Response, error) {
				once.Do(func() { close(started) })
				<-r.Context().Done()
				close(ended)
				return nil, r.Context().Err()
			})
			stop := runCallbacks(t, &CallbackWorker{Store: store, Destinations: map[string]string{"updates": "https://callback.example"}, Transport: transport, PollInterval: 10 * time.Millisecond})
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("attempt not started")
			}
			if cancelSubscription {
				require.NoError(t, store.CancelCallback(t.Context(), front.ID, sub.ID))
			} else {
				stop()
			}
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Fatal("attempt outlived owner cancellation")
			}
			if cancelSubscription {
				require.Eventually(t, func() bool {
					d, e := store.CallbackDeliveries(t.Context(), front.ID, sub.ID)
					return e == nil && len(d) == 1 && d[0].State == "cancelled"
				}, time.Second, 10*time.Millisecond)
			}
			stop()
			subs, err := store.CallbackSubscriptions(t.Context(), front.ID)
			require.NoError(t, err)
			require.Zero(t, subs[0].Cursor)
		})
	}
}

func TestCallbackDestinationsAndAddressProtection(t *testing.T) {
	for _, address := range []string{"http://example.com", "https://user:password@example.com", "https://example.com/#fragment", "https://localhost:8080", "https://127.0.0.1", "https://[::1]", "https://169.254.169.254", "https://100.64.0.1", "https://[::ffff:127.0.0.1]"} {
		require.Error(t, ValidateCallbackDestinations(map[string]string{"updates": address}), address)
	}
	require.NoError(t, ValidateCallbackDestinations(map[string]string{"updates": "https://example.com/callback"}))
	for _, address := range []string{"0.0.0.0", "127.0.0.1", "10.1.1.1", "169.254.169.254", "192.0.2.1", "198.18.0.1", "224.0.0.1", "100.64.0.1", "::1", "::127.0.0.1", "::10.0.0.1", "100::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "fec0::1", "2001:db8::1", "3fff::1"} {
		require.False(t, callbackPublicIP(netip.MustParseAddr(address)), address)
	}
	require.True(t, callbackPublicIP(netip.MustParseAddr("8.8.8.8")))
	require.True(t, callbackPublicIP(netip.MustParseAddr("2606:4700:4700::1111")))
	transport := newCallbackTransport()
	require.Nil(t, transport.Proxy)
	require.True(t, transport.DisableKeepAlives)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := transport.DialContext(ctx, "tcp", "localhost:443")
	require.ErrorContains(t, err, "protected address")
	// Redirects never trigger a second transport call, including a private Location.
	var requests atomic.Int32
	client := &http.Client{Transport: callbackRoundTripper(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		r := callbackResponse(302)
		r.Header.Set("Location", "https://127.0.0.1/private")
		return r, nil
	}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get("https://example.com/events")
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, int32(1), requests.Load())
}

func TestCallbackTransportPinsPublicDNSAndPreservesTLSHostname(t *testing.T) {
	var sni atomic.Value
	endpoint := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	endpoint.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { sni.Store(hello.ServerName); return nil, nil }}
	endpoint.StartTLS()
	defer endpoint.Close()
	var lookups, dials atomic.Int32
	transport := newCallbackTransportWithNetwork(func(context.Context, string, string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		if address != "8.8.8.8:443" {
			return nil, errors.New("socket was not pinned")
		}
		return (&net.Dialer{}).DialContext(ctx, network, endpoint.Listener.Addr().String())
	})
	transport.TLSClientConfig.RootCAs = endpoint.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get("https://example.com/callback")
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, "example.com", sni.Load())
	_, err = client.Get("https://example.com/callback")
	require.ErrorContains(t, err, "protected address")
	require.Equal(t, int32(1), dials.Load(), "mixed public/private DNS must be rejected before any socket dial")
	require.Equal(t, int32(2), lookups.Load(), "every attempt must resolve again, without pooled bypass")
}

func TestCallbackLostSecretRotationFencesOldOwnerAndRevocationAllowsResubscribe(t *testing.T) {
	store, front, _, _ := fixture(t)
	sub, lost := callbackSub(t, store, front)
	replacement, err := store.RotateCallbackKey(t.Context(), front.ID, sub.ID)
	require.NoError(t, err)
	require.NotEqual(t, lost, replacement)
	_, err = store.Send(t.Context(), front.ID, request("key-rotation"))
	require.NoError(t, err)
	old, err := store.claimCallback(t.Context(), "old-owner")
	require.NoError(t, err)
	require.NotNil(t, old)
	began, ended := make(chan struct{}), make(chan struct{})
	result := make(chan string, 1)
	worker := &CallbackWorker{Store: store, Destinations: map[string]string{"updates": "https://callback.example"}}
	client := &http.Client{Transport: callbackRoundTripper(func(r *http.Request) (*http.Response, error) {
		close(began)
		<-r.Context().Done()
		close(ended)
		return nil, r.Context().Err()
	})}
	go func() { _, failure, _ := worker.attempt(t.Context(), client, old, time.Second); result <- failure }()
	<-began
	rotated, err := store.RotateCallbackKey(t.Context(), front.ID, sub.ID)
	require.NoError(t, err)
	require.NotEqual(t, replacement, rotated)
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("old signing generation was not canceled")
	}
	<-result
	require.ErrorIs(t, store.finishCallback(t.Context(), old, 204, "", false, false), ErrLeaseLost)
	fresh, err := store.claimCallback(t.Context(), "new-owner")
	require.NoError(t, err)
	require.NotNil(t, fresh)
	secret, err := hex.DecodeString(rotated)
	require.NoError(t, err)
	require.Equal(t, secret, fresh.secret)
	require.Equal(t, old.body, fresh.body)
	require.Equal(t, old.sequence, fresh.sequence)
	require.Greater(t, fresh.generation, old.generation)
	require.NoError(t, store.finishCallback(t.Context(), fresh, 204, "", false, false))
	require.NoError(t, store.CancelCallback(t.Context(), front.ID, sub.ID))
	next, newSecret, err := store.SubscribeCallback(t.Context(), front.ID, "updates", fresh.sequence)
	require.NoError(t, err)
	require.NotEqual(t, sub.ID, next.ID)
	require.NotEqual(t, rotated, newSecret)
	// Historical deliveries remain inspectable under the revoked subscription.
	history, err := store.CallbackDeliveries(t.Context(), front.ID, sub.ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, "delivered", history[0].State)
	_, err = store.RotateCallbackKey(t.Context(), front.ID, sub.ID)
	require.ErrorIs(t, err, ErrConflict)
	_, err = store.RotateCallbackKey(t.Context(), "foreign", next.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCallbackFairnessDeliversSecondSubscriberBeforeFirstBacklogDrains(t *testing.T) {
	store, front, back, _ := fixture(t)
	first, _ := callbackSub(t, store, front)
	second, _ := callbackSub(t, store, back)
	for index := range 20 {
		_, err := store.Send(t.Context(), front.ID, request(fmt.Sprintf("backlog-%d", index)))
		require.NoError(t, err)
	}
	var firstCalls, secondCalls atomic.Int32
	transport := callbackRoundTripper(func(r *http.Request) (*http.Response, error) {
		id := r.Header.Get("X-Overkill-Event-ID")
		if strings.HasPrefix(id, first.ID+":") {
			firstCalls.Add(1)
		}
		if strings.HasPrefix(id, second.ID+":") {
			secondCalls.Add(1)
		}
		return callbackResponse(204), nil
	})
	stop := runCallbacks(t, &CallbackWorker{Store: store, Destinations: map[string]string{"updates": "https://callback.example"}, Transport: transport, PollInterval: 10 * time.Millisecond})
	require.Eventually(t, func() bool { return secondCalls.Load() > 0 }, time.Second, 5*time.Millisecond)
	stop()
	require.Less(t, firstCalls.Load(), int32(20), "oldest subscriber must not monopolize the worker")
	require.Greater(t, firstCalls.Load(), int32(0))
	require.Greater(t, secondCalls.Load(), int32(0))
}
