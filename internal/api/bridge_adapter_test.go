package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
	bridgegw "github.com/Sahaj-Tech-ltd/overkill/internal/gateway/bridge"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type bridgeFrames struct{ events chan bridgegw.OutboundFrame }

func bridgeSubscribe(t *testing.T, s *Server) *bridgeFrames {
	t.Helper()
	s.mu.RLock()
	address := s.bridgeAddress
	s.mu.RUnlock()
	require.NotEmpty(t, address)
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/v1/out?channel=fixture", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer test-bridge")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	out := &bridgeFrames{events: make(chan bridgegw.OutboundFrame, 256)}
	go func() {
		defer close(out.events)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				var frame bridgegw.OutboundFrame
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) == nil {
					select {
					case out.events <- frame:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	t.Cleanup(func() { cancel(); response.Body.Close() })
	return out
}
func (f *bridgeFrames) await(t *testing.T, chat string, match func(bridgegw.OutboundFrame) bool) bridgegw.OutboundFrame {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case frame, ok := <-f.events:
			if !ok {
				t.Fatal("bridge SSE closed")
			}
			if frame.ChatKey == chat && match(frame) {
				return frame
			}
		case <-timeout.C:
			t.Fatalf("no expected bridge frame for %s", chat)
		}
	}
}
func bridgePost(t *testing.T, s *Server, chat, text string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"channel": "fixture", "chat": chat, "from": "owner", "text": text, "is_direct": true, "workspace": "/incoming-payload-must-not-control-workspace", "session_id": "impersonated"})
	require.NoError(t, err)
	s.mu.RLock()
	address := s.bridgeAddress
	s.mu.RUnlock()
	request, err := http.NewRequest(http.MethodPost, address+"/v1/in", bytes.NewReader(payload))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer test-bridge")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	require.Equal(t, http.StatusAccepted, response.StatusCode, string(body))
}
func bridgeTestConfig(t *testing.T, providerURL string) *config.Config {
	t.Helper()
	t.Setenv("OVERKILL_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "gpt-4o-mini"
	cfg.Agent.MaxTurns = 4
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: providerURL}}
	cfg.Gateways.Bridge = config.BridgeConfig{Enabled: true, Listen: "127.0.0.1:0", Token: "test-bridge", Workspace: t.TempDir()}
	cfg.Gateways.RateLimitPerMin = 100
	return cfg
}
func TestAPIBridgeFirstInboundPersistsSessionAndActualReply(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"actual bridge reply"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(provider.Close)
	cfg := bridgeTestConfig(t, provider.URL)
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	goalStore, err := agent.NewGoalStore(database)
	require.NoError(t, err)
	s := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(context.Context, *session.Session) (*SessionRuntime, error) {
		return &SessionRuntime{Tools: tools.NewRegistry(), GoalStore: goalStore}, nil
	}})
	startHTTPServer(t, s)
	frames := bridgeSubscribe(t, s)
	bridgePost(t, s, "new-chat", "hello")
	final := frames.await(t, "new-chat", func(f bridgegw.OutboundFrame) bool { return f.Kind == "final" })
	require.Equal(t, "actual bridge reply", final.Text)
	path := filepath.Join(mustBridgeHome(t), "gateway-router.json")
	router, err := gateway.NewSessionRouter(path)
	require.NoError(t, err)
	sid, _ := router.Resolve("bridge:fixture", "new-chat", "", "")
	require.NotEmpty(t, sid)
	require.NotEqual(t, "impersonated", sid)
	saved, err := store.Load(t.Context(), sid)
	require.NoError(t, err)
	expectedWorkspace, err := filepath.EvalSymlinks(cfg.Gateways.Bridge.Workspace)
	require.NoError(t, err)
	require.Equal(t, expectedWorkspace, saved.Folder)
	require.Len(t, saved.Messages, 2)
	require.Equal(t, "actual bridge reply", saved.Messages[1].Content)
	// Reconstruct the actual bridge entrypoint with its persisted router/store.
	fresh, err := s.newAPIBridge(t.Context())
	require.NoError(t, err)
	again, _ := fresh.Dispatcher.Router.Resolve("bridge:fixture", "new-chat", "", "")
	require.Equal(t, sid, again)
	bridgePost(t, s, "new-chat", "/goal set remember the target")
	frames.await(t, "new-chat", func(f bridgegw.OutboundFrame) bool { return f.Kind == "final" && strings.Contains(f.Text, "goal set") })
	goal, err := goalStore.Get(t.Context(), sid)
	require.NoError(t, err)
	require.Equal(t, "remember the target", goal.Text)
	bridgePost(t, s, "new-chat", "/goal")
	frames.await(t, "new-chat", func(f bridgegw.OutboundFrame) bool {
		return f.Kind == "final" && strings.Contains(f.Text, "current goal: remember the target")
	})
	bridgePost(t, s, "new-chat", "/goal pause")
	frames.await(t, "new-chat", func(f bridgegw.OutboundFrame) bool {
		return f.Kind == "final" && strings.Contains(f.Text, "goal paused")
	})
	goal, err = goalStore.Get(t.Context(), sid)
	require.NoError(t, err)
	require.False(t, goal.Active)
	bridgePost(t, s, "new-chat", "/goal resume")
	frames.await(t, "new-chat", func(f bridgegw.OutboundFrame) bool {
		return f.Kind == "final" && strings.Contains(f.Text, "goal resumed")
	})
	goal, err = goalStore.Get(t.Context(), sid)
	require.NoError(t, err)
	require.True(t, goal.Active)
	bridgePost(t, s, "new-chat", "/goal clear")
	frames.await(t, "new-chat", func(f bridgegw.OutboundFrame) bool {
		return f.Kind == "final" && strings.Contains(f.Text, "goal cleared")
	})
	goal, err = goalStore.Get(t.Context(), sid)
	require.NoError(t, err)
	require.Nil(t, goal)
	for _, command := range []string{"/snapshot", "/rollback", "/fork", "/retry"} {
		bridgePost(t, s, "new-chat", command)
		frames.await(t, "new-chat", func(f bridgegw.OutboundFrame) bool {
			return f.Kind == "final" && strings.Contains(f.Text, "unsupported")
		})
	}
	bridgePost(t, s, "new-chat", "/undo")
	frames.await(t, "new-chat", func(f bridgegw.OutboundFrame) bool { return f.Kind == "final" && strings.Contains(f.Text, "Undone") })
	saved, err = store.Load(t.Context(), sid)
	require.NoError(t, err)
	require.Empty(t, saved.Messages)
}
func mustBridgeHome(t *testing.T) string {
	t.Helper()
	home, err := config.ConfigDir()
	require.NoError(t, err)
	return home
}

type bridgeHeldTool struct {
	started   chan struct{}
	cancelled chan struct{}
	once      sync.Once
}

func (b *bridgeHeldTool) Name() string { return "bridge_hold" }
func (b *bridgeHeldTool) Execute(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	close(b.cancelled)
	return nil, ctx.Err()
}
func (b *bridgeHeldTool) InterruptBehavior() tools.InterruptBehavior { return tools.InterruptCancel }

func TestAPIBridgeStopCancelsExactChatProviderAndTool(t *testing.T) {
	for _, heldTool := range []bool{false, true} {
		t.Run(fmt.Sprintf("tool=%t", heldTool), func(t *testing.T) {
			enteredA, enteredB, cancelledA, cancelledB := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var onceA, onceB sync.Once
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				if bytes.Contains(body, []byte("hold-A")) {
					if heldTool {
						_, _ = io.WriteString(w, `{"id":"tool","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"hold","type":"function","function":{"name":"bridge_hold","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
						return
					}
					onceA.Do(func() { close(enteredA) })
					<-r.Context().Done()
					close(cancelledA)
					return
				}
				onceB.Do(func() { close(enteredB) })
				<-r.Context().Done()
				close(cancelledB)
			}))
			t.Cleanup(provider.Close)
			cfg := bridgeTestConfig(t, provider.URL)
			database, _ := testpg.Open(t)
			store, err := session.NewPostgresStore(database)
			require.NoError(t, err)
			tool := &bridgeHeldTool{started: enteredA, cancelled: cancelledA}
			s := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(context.Context, *session.Session) (*SessionRuntime, error) {
				registry := tools.NewRegistry()
				if heldTool {
					require.NoError(t, registry.Register(tool))
				}
				return &SessionRuntime{Tools: registry}, nil
			}})
			startHTTPServer(t, s)
			frames := bridgeSubscribe(t, s)
			bridgePost(t, s, "A", "hold-A")
			awaitBridgeSignal(t, enteredA, "A run start")
			bridgePost(t, s, "B", "hold-B")
			awaitBridgeSignal(t, enteredB, "B run start")
			bridgePost(t, s, "A", "/goal set must-not-mutate-while-busy")
			frames.await(t, "A", func(f bridgegw.OutboundFrame) bool {
				return f.Kind == "final" && strings.Contains(f.Text, "currently working")
			})
			bridgePost(t, s, "A", "/stop")
			awaitBridgeSignal(t, cancelledA, "A context cancellation")
			select {
			case <-cancelledB:
				t.Fatal("A stop cancelled B")
			case <-time.After(50 * time.Millisecond):
			}
			frames.await(t, "A", func(f bridgegw.OutboundFrame) bool {
				return f.Kind == "error" && strings.Contains(strings.ToLower(f.Text), "cancel")
			})
			bridgePost(t, s, "B", "/stop")
			awaitBridgeSignal(t, cancelledB, "B cleanup cancellation")
			frames.await(t, "B", func(f bridgegw.OutboundFrame) bool { return f.Kind == "error" })
		})
	}
}
func awaitBridgeSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(label + " timed out")
	}
}

func TestAPIBridgeFailsClosedOnRouterAndWorkspaceErrors(t *testing.T) {
	cfg := bridgeTestConfig(t, "")
	s := NewServer(ServerConfig{Config: cfg, SessionStore: &mockSessionStore{sessions: map[string]*session.Session{}}})
	home := mustBridgeHome(t)
	require.NoError(t, os.WriteFile(filepath.Join(home, "gateway-router.json"), []byte("invalid json"), 0600))
	_, err := s.newAPIBridge(t.Context())
	require.ErrorContains(t, err, "decode router")
	require.NoError(t, os.Remove(filepath.Join(home, "gateway-router.json")))
	cfg.Gateways.Bridge.Workspace = filepath.Join(home, "missing")
	_, err = s.newAPIBridge(t.Context())
	require.ErrorContains(t, err, "workspace")
}

func TestAPIBridgeGoalAndUnsupportedOperationsFailExplicitly(t *testing.T) {
	cfg := bridgeTestConfig(t, "http://127.0.0.1:1")
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	s := NewServer(ServerConfig{Config: cfg, SessionStore: store})
	t.Cleanup(s.Close)
	b, err := s.newAPIBridge(t.Context())
	require.NoError(t, err)
	bound, err := b.Dispatcher.Agent.(gateway.SessionAgentProvider).AgentForSession(t.Context(), "bridge:fixture-explicit")
	require.NoError(t, err)
	require.ErrorContains(t, bound.SetGoal(t.Context(), "must-not-fake"), "goal store not wired")
	_, err = bound.GetGoal(t.Context())
	require.ErrorContains(t, err, "goal store not wired")
	require.ErrorContains(t, bound.PauseGoal(t.Context()), "goal store not wired")
	require.ErrorContains(t, bound.ResumeGoal(t.Context()), "goal store not wired")
	require.ErrorContains(t, bound.ClearGoal(t.Context()), "goal store not wired")
	unsupported := []func() (string, error){func() (string, error) { return bound.Retry(t.Context()) }, func() (string, error) { return bound.Fork("x") }, func() (string, error) { return bound.Snapshot("x") }, func() (string, error) { return bound.Rollback(0) }, bound.Snapshots, func() (string, error) { return bound.ExportHistory("ignored") }}
	for _, call := range unsupported {
		result, err := call()
		require.Empty(t, result)
		require.ErrorContains(t, err, "unsupported")
	}
	// The reported status includes persistence/admission, even if no model call is active.
	release, err := s.beginSessionOperation(bound.SessionID())
	require.NoError(t, err)
	require.True(t, bound.IsBusy())
	require.ErrorContains(t, bound.(gateway.SessionStopper).InterruptSession(), "another entry point")
	release()
	require.False(t, bound.IsBusy())
}

func TestAPIBridgeShutdownCancelsAcceptedProviderBeforeRuntimeCleanup(t *testing.T) {
	entered, stopped := make(chan struct{}), make(chan struct{})
	var enteredOnce, stoppedOnce sync.Once
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		enteredOnce.Do(func() { close(entered) })
		<-r.Context().Done()
		stoppedOnce.Do(func() { close(stopped) })
	}))
	t.Cleanup(provider.Close)
	cfg := bridgeTestConfig(t, provider.URL)
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	var cleanupBeforeSave bool
	var cleanupCalled bool
	var cleanupMu sync.Mutex
	s := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(_ context.Context, metadata *session.Session) (*SessionRuntime, error) {
		return &SessionRuntime{Tools: tools.NewRegistry(), Cleanup: func() {
			saved, err := store.Load(context.Background(), metadata.ID)
			cleanupMu.Lock()
			cleanupCalled = true
			cleanupBeforeSave = err != nil || len(saved.Messages) == 0
			cleanupMu.Unlock()
		}}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()
	select {
	case <-s.Ready():
	case err := <-done:
		t.Fatalf("startup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup timed out")
	}
	_ = bridgeSubscribe(t, s)
	bridgePost(t, s, "shutdown", "hold-A")
	awaitBridgeSignal(t, entered, "accepted provider start")
	cancel()
	awaitBridgeSignal(t, stopped, "provider shutdown cancellation")
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("API shutdown did not drain bridge")
	}
	cleanupMu.Lock()
	premature := cleanupBeforeSave
	cleaned := cleanupCalled
	cleanupMu.Unlock()
	require.True(t, cleaned, "runtime cleanup not called")
	require.False(t, premature, "runtime cleaned before accepted work persisted cancellation")
}
