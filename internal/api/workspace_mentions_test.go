package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestAPIWorkspaceMentionsRunAndStream(t *testing.T) {
	ambient, external := t.TempDir(), t.TempDir()
	t.Chdir(ambient)
	require.NoError(t, os.WriteFile(filepath.Join(ambient, "context.txt"), []byte("AMBIENT-CONTENT-MUST-NOT-LEAK"), 0600))
	externalFile := filepath.Join(external, "secret.txt")
	require.NoError(t, os.WriteFile(externalFile, []byte("EXTERNAL-CONTENT-MUST-NOT-LEAK"), 0600))
	requests := make(chan string, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream   bool                             `json:"stream"`
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		var contextBlock strings.Builder
		for _, m := range body.Messages {
			if m.Role == "system" && strings.Contains(m.Content, "files referenced via @path:") {
				contextBlock.WriteString(m.Content)
			}
		}
		requests <- contextBlock.String()
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Workspace context observed.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Workspace context observed."},"finish_reason":"stop"}]}`)
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "gpt-4o-mini"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fixture-only", BaseURL: upstream.URL}}
	store := &mockSessionStore{sessions: map[string]*session.Session{}}
	s := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(context.Context, *session.Session) (*SessionRuntime, error) {
		return &SessionRuntime{Tools: tools.NewRegistry()}, nil
	}})
	defer s.Close()
	for _, name := range []string{"workspace-a", "workspace-b"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			marker := "SELECTED-" + name
			require.NoError(t, os.WriteFile(filepath.Join(root, "context.txt"), []byte(marker), 0600))
			require.NoError(t, os.Symlink(externalFile, filepath.Join(root, "escape.txt")))
			large, err := os.Create(filepath.Join(root, "large.txt"))
			require.NoError(t, err)
			_, err = large.Write([]byte("BOUNDED-LARGE-PREFIX"))
			require.NoError(t, err)
			require.NoError(t, large.Truncate(1<<30))
			require.NoError(t, large.Close())
			metadata := session.NewSession(root)
			require.NoError(t, store.Create(t.Context(), metadata))
			a, rpcErr := s.getOrCreateAgent(t.Context(), metadata.ID)
			require.Nil(t, rpcErr)
			input := "Inspect @context.txt @escape.txt @large.txt @" + externalFile
			for _, stream := range []bool{false, true} {
				label := "run"
				if stream {
					label = "stream"
				}
				t.Run(label, func(t *testing.T) {
					if stream {
						events, err := a.Stream(t.Context(), input)
						require.NoError(t, err)
						done := false
						for e := range events {
							require.NoError(t, e.Error)
							if e.Type == agent.EventDone {
								done = true
							}
						}
						require.True(t, done)
					} else {
						_, err := a.Run(t.Context(), input)
						require.NoError(t, err)
					}
					block := <-requests
					require.Contains(t, block, marker)
					require.NotContains(t, block, "AMBIENT-CONTENT-MUST-NOT-LEAK")
					require.NotContains(t, block, "EXTERNAL-CONTENT-MUST-NOT-LEAK")
					require.NotContains(t, block, "SELECTED-workspace-"+map[string]string{"workspace-a": "b", "workspace-b": "a"}[name])
					require.Contains(t, block, "BOUNDED-LARGE-PREFIX")
					require.Contains(t, block, "[truncated at 16384 bytes]")
					require.Less(t, len(block), 40000)
				})
			}
		})
	}
}
