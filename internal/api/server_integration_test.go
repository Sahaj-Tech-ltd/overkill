package api

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type streamingTestProvider struct{}

func (streamingTestProvider) Name() string              { return "fixture" }
func (streamingTestProvider) Models() []providers.Model { return nil }
func (streamingTestProvider) Complete(context.Context, providers.Request) (providers.Response, error) {
	return providers.Response{Content: "hello"}, nil
}
func (streamingTestProvider) Stream(context.Context, providers.Request) (<-chan providers.Chunk, error) {
	ch := make(chan providers.Chunk, 2)
	ch <- providers.Chunk{Content: "hello"}
	ch <- providers.Chunk{Done: true}
	close(ch)
	return ch, nil
}

func startHTTPServer(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- s.Start(ctx) }()
	select {
	case <-s.Ready():
	case err := <-result:
		cancel()
		t.Fatalf("startup: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("server never ready")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-result:
			require.NoError(t, err)
		case <-time.After(6 * time.Second):
			t.Error("server did not shut down")
		}
	})
}

func TestServerAuthenticatedRoutesAndStreaming(t *testing.T) {
	for _, configured := range []string{"", "configured-credential"} {
		t.Run(fmt.Sprintf("configured=%t", configured != ""), func(t *testing.T) {
			t.Setenv("OVERKILL_API_TOKEN", configured)
			store := &mockSessionStore{sessions: make(map[string]*session.Session)}
			s := NewServer(ServerConfig{Config: config.Default(), SessionStore: store})
			credential := s.AuthToken()
			require.NotEmpty(t, credential)
			if configured != "" {
				require.Equal(t, configured, credential)
			} else {
				require.Len(t, credential, 64)
			}
			for _, id := range []string{"sse-test", "stream-test"} {
				sess := session.NewSession(t.TempDir())
				sess.ID = id
				require.NoError(t, store.Create(context.Background(), sess))
				a := agent.New(agent.Config{Provider: streamingTestProvider{}, Tools: tools.NewRegistry(), Model: "fixture", SessionID: id})
				s.agents[id] = a
				t.Cleanup(a.Shutdown)
			}
			startHTTPServer(t, s)
			client := &http.Client{Timeout: 5 * time.Second}
			for _, route := range []struct{ path, method, body string }{
				{"/health", "GET", ""},
				{"/rpc", "POST", `{"jsonrpc":"2.0","id":1,"method":"config.exists"}`},
				{"/sse?session=sse-test&message=test", "GET", ""},
				{"/stream?session_id=stream-test&message=test", "GET", ""},
			} {
				for _, token := range []string{"", "wrong", credential} {
					req, err := http.NewRequest(route.method, s.Addr()+route.path, strings.NewReader(route.body))
					require.NoError(t, err)
					if token != "" {
						req.Header.Set("Authorization", "Bearer "+token)
					}
					res, err := client.Do(req)
					require.NoError(t, err)
					body, err := io.ReadAll(res.Body)
					res.Body.Close()
					require.NoError(t, err)
					if token != credential {
						require.Equal(t, http.StatusUnauthorized, res.StatusCode)
						continue
					}
					require.Equal(t, http.StatusOK, res.StatusCode, string(body))
					if strings.HasPrefix(route.path, "/s") {
						require.Contains(t, res.Header.Get("Content-Type"), "text/event-stream")
						require.Contains(t, string(body), "hello")
					}
				}
			}
			require.Equal(t, credential, s.AuthToken())
		})
	}
}

func TestServerBindFailureDoesNotSignalReady(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	s := NewServer(ServerConfig{Config: config.Default(), ListenAddr: listener.Addr().String()})
	require.ErrorContains(t, s.Start(context.Background()), "listen")
	select {
	case <-s.Ready():
		t.Fatal("failed server signaled ready")
	default:
	}
}

// Run with Node.js on PATH and npm ci in tui/. CI must run this test with
// OVERKILL_REQUIRE_FRONTEND_TEST=1 so missing runtime dependencies fail the gate.
func TestFrontendClientAuthenticatedHTTP(t *testing.T) {
	node, err := exec.LookPath("node")
	loader, pathErr := filepath.Abs("../../tui/node_modules/tsx/dist/loader.mjs")
	if pathErr == nil {
		_, pathErr = os.Stat(loader)
	}
	if err != nil || pathErr != nil {
		if os.Getenv("OVERKILL_REQUIRE_FRONTEND_TEST") == "1" {
			t.Fatalf("frontend test requires node and tui npm ci: %v / %v", err, pathErr)
		}
		t.Skip("node and frontend dependencies required; CI enforces this integration test")
	}
	t.Setenv("OVERKILL_API_TOKEN", "")
	store := &mockSessionStore{sessions: make(map[string]*session.Session)}
	s := NewServer(ServerConfig{Config: config.Default(), SessionStore: store})
	sess := session.NewSession(t.TempDir())
	sess.ID = "client-test"
	require.NoError(t, store.Create(context.Background(), sess))
	a := agent.New(agent.Config{Provider: streamingTestProvider{}, Tools: tools.NewRegistry(), Model: "fixture", SessionID: sess.ID})
	s.agents[sess.ID] = a
	t.Cleanup(a.Shutdown)
	startHTTPServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	script, err := filepath.Abs("../../tui/tests/backend-smoke.ts")
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, node, "--import", loader, script)
	cmd.Env = append(os.Environ(), "OVERKILL_API_URL="+s.Addr(), "OVERKILL_API_TOKEN="+s.AuthToken())
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "Frontend authenticated RPC, health and streaming passed")
}

func TestMiddlewareFlushesBeforeHandlerReturns(t *testing.T) {
	t.Setenv("OVERKILL_API_TOKEN", "stream-credential")
	s := NewServer(ServerConfig{Config: config.Default()})
	release := make(chan struct{})
	ts := httptest.NewServer(s.withMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
	}))
	defer ts.Close()
	defer close(release)
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodGet, ts.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+s.AuthToken())
	res, err := client.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	first := make([]byte, len("data: first\n\n"))
	_, err = io.ReadFull(res.Body, first)
	require.NoError(t, err)
	require.Equal(t, "data: first\n\n", string(first))
}
