package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type admittedSnapshotter struct {
	calls atomic.Int32
	err   error
}

func (s *admittedSnapshotter) Snapshot(_ string, _ string, _ []string) error {
	s.calls.Add(1)
	return s.err
}

func TestAPIFactoryCheckpointHookIsAutomaticAndFailClosed(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "capture", true: "failure"}[fail], func(t *testing.T) {
			var requests atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if requests.Add(1) == 1 {
					io.WriteString(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"write","type":"function","function":{"name":"fs","arguments":"{\"action\":\"write\",\"path\":\"written.txt\",\"content\":\"after\"}"}}]},"finish_reason":"tool_calls"}]}`)
				} else {
					io.WriteString(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"Finished"},"finish_reason":"stop"}]}`)
				}
			}))
			defer provider.Close()
			cfg := config.Default()
			cfg.Agent.DefaultProvider = "fixture"
			cfg.Agent.DefaultModel = "gpt-4o-mini"
			cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: provider.URL}}
			metadata := session.NewSession(t.TempDir())
			require.NoError(t, os.WriteFile(filepath.Join(metadata.Folder, "written.txt"), []byte("before"), 0o600))
			store := &mockSessionStore{sessions: map[string]*session.Session{metadata.ID: metadata}}
			snapshotter := &admittedSnapshotter{}
			if fail {
				snapshotter.err = errors.New("test recovery failure")
			}
			server := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(_ context.Context, metadata *session.Session) (*SessionRuntime, error) {
				registry := tools.NewRegistry()
				require.NoError(t, registry.Register(tools.NewFSTool(metadata.Folder)))
				return &SessionRuntime{Tools: registry, CheckpointSnapshotter: snapshotter}, nil
			}})
			defer server.Close()
			input, _ := json.Marshal(map[string]string{"session_id": metadata.ID, "message": "Write the requested file"})
			_, rpcErr := server.handleAgentSend(t.Context(), input)
			require.Nil(t, rpcErr)
			require.EqualValues(t, 1, snapshotter.calls.Load())
			data, err := os.ReadFile(filepath.Join(metadata.Folder, "written.txt"))
			require.NoError(t, err)
			if fail {
				require.Equal(t, "before", string(data))
			} else {
				require.Equal(t, "after", string(data))
			}
		})
	}
}
