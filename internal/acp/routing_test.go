package acp

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type gatedSender struct {
	id      string
	release chan struct{}
}

func (s *gatedSender) Model() string     { return "scripted" }
func (s *gatedSender) SessionID() string { return s.id }
func (s *gatedSender) StreamACP(ctx context.Context, input string) (<-chan AgentEvent, error) {
	events := make(chan AgentEvent)
	go func() {
		defer close(events)
		select {
		case events <- AgentEvent{Type: AgentEventToken, Content: s.id + ":" + input}:
		case <-ctx.Done():
			return
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return
		}
		select {
		case events <- AgentEvent{Type: AgentEventDone}:
		case <-ctx.Done():
		}
	}()
	return events, nil
}

func TestRoutingRunsDistinctSessionsAndRejectsOverlap(t *testing.T) {
	frontend := &gatedSender{id: "frontend", release: make(chan struct{})}
	backend := &gatedSender{id: "backend", release: make(chan struct{})}
	server := NewServer(Config{Token: "test", ResolveSender: func(ctx context.Context, id string) (Sender, error) {
		switch id {
		case frontend.id:
			return frontend, nil
		case backend.id:
			return backend, nil
		default:
			return nil, fmt.Errorf("unknown session")
		}
	}})
	defer server.Shutdown(context.Background())
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	client := NewClient(httpServer.URL, "test")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	frontEvents, err := client.SendTo(ctx, "frontend", "UI question")
	require.NoError(t, err)
	backEvents, err := client.SendTo(ctx, "backend", "API question")
	require.NoError(t, err)
	_, err = client.SendTo(ctx, "frontend", "overlap")
	require.ErrorContains(t, err, "409")
	close(frontend.release)
	close(backend.release)
	for id, events := range map[string]<-chan Event{"frontend": frontEvents, "backend": backEvents} {
		var text strings.Builder
		done := false
		for event := range events {
			require.NotEqual(t, "error", event.Type, "%s", event.Error)
			if event.Type == "text_delta" {
				text.WriteString(event.Content)
			}
			if event.Type == "done" {
				done = true
			}
		}
		require.True(t, done)
		require.Contains(t, text.String(), id+":")
	}
}

func TestLegacySenderRejectsDifferentRequestedIdentity(t *testing.T) {
	server := NewServer(Config{Token: "test", Agent: &fakeAgent{}})
	defer server.Shutdown(context.Background())
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	_, err := NewClient(httpServer.URL, "test").SendTo(t.Context(), "other-session", "message")
	require.ErrorContains(t, err, "400")
}

func TestStartReportsBindFailureAndActualEphemeralAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	blocked := NewServer(Config{Addr: listener.Addr().String(), Token: "test"})
	defer blocked.Shutdown(context.Background())
	require.ErrorContains(t, blocked.Start(), "listen")
	server := NewServer(Config{Addr: "127.0.0.1:0", Token: "test"})
	require.NoError(t, server.Start())
	defer server.Shutdown(context.Background())
	require.NotEqual(t, "127.0.0.1:0", server.Addr())
	_, err = NewClient("http://"+server.Addr(), "test").GetInfo(t.Context())
	require.NoError(t, err)
}

func TestBufferedStreamDeliversEveryEventWithoutSilentLoss(t *testing.T) {
	chunks := make([]AgentEvent, 257)
	for i := 0; i < len(chunks)-1; i++ {
		chunks[i] = AgentEvent{Type: AgentEventToken, Content: "x"}
	}
	chunks[len(chunks)-1] = AgentEvent{Type: AgentEventDone}
	server := NewServer(Config{Token: "test", Agent: &fakeAgent{chunks: chunks}})
	defer server.Shutdown(context.Background())
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	events, err := NewClient(httpServer.URL, "test").Send(ctx, "more than a channel buffer")
	require.NoError(t, err)
	count := 0
	for event := range events {
		require.NotEqual(t, "error", event.Type, "%s", event.Error)
		count++
	}
	require.Equal(t, len(chunks), count)
}
