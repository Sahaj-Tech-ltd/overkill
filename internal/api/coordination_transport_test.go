package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestRuntimeTransportChangeCannotDiscardActiveTurnFromReaders(t *testing.T) {
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	metadata := session.NewSession(t.TempDir())
	metadata.Provider = "fixture"
	metadata.Model = "fixture"
	require.NoError(t, sessions.Create(t.Context(), metadata))
	privateFile := filepath.Join(t.TempDir(), "private-credential")
	require.NoError(t, os.WriteFile(privateFile, []byte("first-private-version"), 0600))
	entered, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"active turn finished safely"},"finish_reason":"stop"}]}`)
		case <-r.Context().Done():
			close(cancelled)
		}
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "fixture"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", BaseURL: provider.URL, APIKey: "fixture-only"}}
	var built, disposed, checked atomic.Int32
	server := NewServer(ServerConfig{Config: cfg, SessionStore: sessions, SessionRuntimeFactory: func(ctx context.Context, s *session.Session) (*SessionRuntime, error) {
		snapshot, err := os.ReadFile(privateFile)
		if err != nil {
			return nil, err
		}
		built.Add(1)
		return &SessionRuntime{Tools: tools.NewRegistry(), CheckCurrent: func(context.Context) (bool, error) {
			checked.Add(1)
			current, err := os.ReadFile(privateFile)
			return string(current) == string(snapshot), err
		}, Cleanup: func() { disposed.Add(1) }}, nil
	}})
	defer server.Close()
	params, _ := json.Marshal(SendMessageParams{SessionID: metadata.ID, Message: "hold active turn"})
	done := make(chan *RPCError, 1)
	go func() { _, rpcErr := server.handleAgentSend(t.Context(), params); done <- rpcErr }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not enter admitted turn")
	}
	require.NoError(t, os.WriteFile(privateFile, []byte("changed-private-version"), 0600))
	_, rpcErr := server.handleExtensions(t.Context(), "extensions.list", coordInputForRuntime(t, map[string]any{"session_id": metadata.ID}))
	require.NotNil(t, rpcErr)
	bridge := &bridgeAgent{state: &bridgeState{server: server, workspace: metadata.Folder, lifecycle: t.Context()}}
	bound, err := bridge.AgentForSession(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.True(t, bound.IsBusy(), "bound control adapter must remain resolvable for stop/busy operations")
	require.Zero(t, disposed.Load(), "non-admitted readers must not discard running agent")
	require.Zero(t, checked.Load(), "busy readers must not evaluate a stale-cache rebuild")
	select {
	case <-cancelled:
		t.Fatal("reader cancelled the admitted provider run")
	default:
	}
	close(release)
	require.Nil(t, <-done)
	loaded, err := sessions.Load(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.Equal(t, "active turn finished safely", loaded.Messages[len(loaded.Messages)-1].Content)
	_, rpcErr = server.getOrCreateAgent(t.Context(), metadata.ID)
	require.Nil(t, rpcErr)
	require.Equal(t, int32(2), built.Load())
	require.Equal(t, int32(1), disposed.Load())
	require.Equal(t, int32(1), checked.Load())
}
