package devbrowser

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

// These tests verify refusal, not a working isolated browser. The controlled
// pages must never be fetched, so their redirects/scripts/subresources cannot
// generate private-network traffic in the unavailable restricted mode.
func TestRestrictedManagerRefusesRedirectsAndSubresourcesBeforeTraffic(t *testing.T) {
	var initialHits, privateHits atomic.Int32
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { privateHits.Add(1); fmt.Fprint(w, "private resource") }))
	defer private.Close()
	publicFixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		initialHits.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, private.URL+"/metadata", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<img src="%s/image"><iframe src="%s/frame"></iframe><script>fetch(%q);new WebSocket(%q)</script>`, private.URL, private.URL, private.URL+"/xhr", "ws"+private.URL[4:]+"/socket")
	}))
	defer publicFixture.Close()
	host, _ := url.Parse(publicFixture.URL)
	for _, raw := range []string{publicFixture.URL + "/redirect", publicFixture.URL + "/subresources", "http://public.invalid:" + host.Port() + "/redirect", "http://public.invalid:" + host.Port() + "/subresources"} {
		manager := NewManager()
		_, err := manager.Open("network-test", raw)
		require.ErrorIs(t, err, ErrNetworkIsolationUnavailable)
		require.Empty(t, manager.ListPages())
		require.Nil(t, manager.parent)
		manager.Shutdown()
	}
	require.Zero(t, initialHits.Load(), "refused restricted mode must not fetch even the initial page")
	require.Zero(t, privateHits.Load(), "redirects and subresources must not reach the private fixture")
}

func TestRestrictedManagerGuardsAllOperationsAndBrowserStartup(t *testing.T) {
	manager := NewManager()
	// A preexisting tab must not enable interactions that can trigger requests.
	// An invalid context makes any accidental browser operation fail this test.
	manager.pages["existing"] = &Page{Name: "existing", cancel: func() {}}
	defer manager.Shutdown()
	_, err := manager.Snapshot("existing")
	require.ErrorIs(t, err, ErrNetworkIsolationUnavailable)
	_, err = manager.Click("existing", "a")
	require.ErrorIs(t, err, ErrNetworkIsolationUnavailable)
	_, err = manager.Type("existing", "input", "submit-trigger")
	require.ErrorIs(t, err, ErrNetworkIsolationUnavailable)
	_, err = manager.ensureBrowser()
	require.ErrorIs(t, err, ErrNetworkIsolationUnavailable)
	require.Nil(t, manager.parent)
}

func TestTrustedNetworkRequiresExplicitApplicationOption(t *testing.T) {
	require.ErrorIs(t, NewManagerWithOptions(Options{}).checkNetworkMode(), ErrNetworkIsolationUnavailable)
	trusted := NewManagerWithOptions(Options{TrustedNetwork: true})
	defer trusted.Shutdown()
	require.NoError(t, trusted.checkNetworkMode())
	// Local/private access is intentionally permitted in this mode. This test
	// checks construction/syntax policy; it does not spawn a real Chrome process.
	for _, raw := range []string{"http://localhost:3000/login", "http://127.0.0.1/", "http://10.0.0.1/"} {
		_, err := parseHTTPURL(raw)
		require.NoError(t, err)
	}
	for _, raw := range []string{"http://operator:secret@localhost/", "file:///etc/passwd", "javascript:alert(1)", "data:text/html,test"} {
		_, err := trusted.Open("trusted", raw)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrNetworkIsolationUnavailable)
		require.Nil(t, trusted.parent)
	}
}

// The DNS fixture demonstrably rebinds an A record from public to loopback on
// successive queries. The default browser must refuse before any resolver call,
// rather than check the first answer and let Chrome resolve the second one.
func TestRestrictedManagerRefusesBeforeRebindingResolution(t *testing.T) {
	socket, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer socket.Close()
	var queries atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		packet := make([]byte, 4096)
		for {
			n, peer, err := socket.ReadFrom(packet)
			if err != nil {
				return
			}
			var request dnsmessage.Message
			if request.Unpack(packet[:n]) != nil || len(request.Questions) == 0 {
				continue
			}
			question := request.Questions[0]
			count := queries.Add(1)
			ip := [4]byte{93, 184, 216, 34}
			if count > 1 {
				ip = [4]byte{127, 0, 0, 1}
			}
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
			response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 0}, Body: &dnsmessage.AResource{A: ip}}}
			wire, err := response.Pack()
			if err == nil {
				_, _ = socket.WriteTo(wire, peer)
			}
		}
	}()
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", socket.LocalAddr().String())
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	first, err := resolver.LookupIP(ctx, "ip4", "rebind.invalid")
	require.NoError(t, err)
	require.Equal(t, "93.184.216.34", first[0].String())
	second, err := resolver.LookupIP(ctx, "ip4", "rebind.invalid")
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", second[0].String())
	queries.Store(0)
	previous := net.DefaultResolver
	net.DefaultResolver = resolver
	defer func() { net.DefaultResolver = previous }()
	manager := NewManager()
	defer manager.Shutdown()
	_, err = manager.Open("rebind", "http://rebind.invalid/resource")
	require.ErrorIs(t, err, ErrNetworkIsolationUnavailable)
	require.Zero(t, queries.Load(), "restricted refusal must precede DNS; no resolve/check/resolve race")
	require.Nil(t, manager.parent)
	require.Empty(t, manager.ListPages())
	require.NoError(t, socket.Close())
	<-done
}
