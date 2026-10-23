package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type abortTurnProvider struct {
	calls   atomic.Int32
	entered chan struct{}
}

func (p *abortTurnProvider) Name() string              { return "abort-fixture" }
func (p *abortTurnProvider) Models() []providers.Model { return nil }
func (p *abortTurnProvider) Complete(ctx context.Context, _ providers.Request) (providers.Response, error) {
	if p.calls.Add(1) == 1 {
		close(p.entered)
		<-ctx.Done()
		return providers.Response{}, ctx.Err()
	}
	return providers.Response{Content: "after cancel"}, nil
}
func (p *abortTurnProvider) Stream(ctx context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
	ch := make(chan providers.Chunk, 1)
	if p.calls.Add(1) == 1 {
		close(p.entered)
		go func() { <-ctx.Done(); close(ch) }()
		return ch, nil
	}
	ch <- providers.Chunk{Content: "after cancel", Done: true}
	close(ch)
	return ch, nil
}
func TestAbortCancelsCurrentOperationAndPermitsNextTurn(t *testing.T) {
	for _, mode := range []string{"rpc", "stream"} {
		t.Run(mode, func(t *testing.T) {
			metadata := session.NewSession(t.TempDir())
			store := &mockSessionStore{sessions: map[string]*session.Session{metadata.ID: metadata}}
			s := NewServer(ServerConfig{Config: config.Default(), SessionStore: store})
			p := &abortTurnProvider{entered: make(chan struct{})}
			a := agent.New(agent.Config{Provider: p, Tools: tools.NewRegistry(), Model: "fixture", SessionID: metadata.ID})
			defer a.Shutdown()
			s.agents[metadata.ID] = a
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request, _ := json.Marshal(map[string]string{"session_id": metadata.ID, "message": "wait"})
			done := make(chan struct{})
			go func() {
				defer close(done)
				if mode == "rpc" {
					_, _ = s.handleAgentSend(ctx, request)
				} else {
					s.handleStream(httptest.NewRecorder(), httptest.NewRequest("GET", "/stream?session_id="+metadata.ID+"&message=wait", nil).WithContext(ctx))
				}
			}()
			select {
			case <-p.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("provider not entered")
			}
			raw, _ := json.Marshal(map[string]string{"session_id": metadata.ID})
			_, rpcErr := s.handleAgentAbort(t.Context(), raw)
			require.Nil(t, rpcErr)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("abort did not cancel active provider")
			}
			select {
			case <-a.StopCh():
				t.Fatal("per-turn abort permanently latched session")
			default:
			}
			result, rpcErr := s.handleAgentSend(t.Context(), request)
			require.Nil(t, rpcErr)
			require.Equal(t, "after cancel", result.(*SendMessageResult).Response)
			require.EqualValues(t, 2, p.calls.Load())
		})
	}
}
func TestDelayedCancellationCleanupCannotRemoveNextGeneration(t *testing.T) {
	s := &Server{}
	first, finishFirst := s.bindSessionCancellation(t.Context(), "same")
	second, finishSecond := s.bindSessionCancellation(t.Context(), "same")
	defer finishSecond()
	finishFirst()
	require.Error(t, first.Err())
	require.NoError(t, second.Err())
	raw := []byte(`{"session_id":"same"}`)
	_, err := s.handleAgentAbort(t.Context(), raw)
	require.Nil(t, err)
	require.Error(t, second.Err())
}
