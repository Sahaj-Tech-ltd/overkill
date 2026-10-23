package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type queueHeldProvider struct {
	entered chan struct{}
	once    sync.Once
}

func (*queueHeldProvider) Name() string              { return "held-queue" }
func (*queueHeldProvider) Models() []providers.Model { return nil }
func (p *queueHeldProvider) Complete(ctx context.Context, _ providers.Request) (providers.Response, error) {
	p.once.Do(func() { close(p.entered) })
	<-ctx.Done()
	return providers.Response{}, ctx.Err()
}
func (*queueHeldProvider) Stream(context.Context, providers.Request) (<-chan providers.Chunk, error) {
	return nil, fmt.Errorf("unexpected stream")
}

func TestSequentialQueueScopesRealActiveQueuesAndIdleSession(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	s := NewServer(ServerConfig{Config: config.Default(), SessionStore: store})
	defer s.closeSessionRuntimes()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	ids := make([]string, 3)
	for i := range ids {
		metadata := session.NewSession(t.TempDir())
		ids[i] = metadata.ID
		require.NoError(t, store.Create(ctx, metadata))
		if i == 2 {
			continue
		}
		p := &queueHeldProvider{entered: make(chan struct{})}
		a := agent.New(agent.Config{Provider: p, Model: "fixture", SessionID: metadata.ID, Tools: tools.NewRegistry(), MaxSteps: 2})
		_, err := a.Run(ctx, "/think")
		require.NoError(t, err)
		s.agents[metadata.ID] = a
		// Poll while sequential queue publication happens, exercising the former
		// lazy-pointer data race as well as actual decomposition/provider execution.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				a.QueueSnapshot()
				time.Sleep(time.Microsecond)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = a.Run(ctx, fmt.Sprintf("Queue %d implement the authentication fixture\n- Queue %d write regression tests for the fixture\n- Queue %d document the completed changes", i, i, i))
		}()
		select {
		case <-p.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("sequential provider did not start")
		}
		require.True(t, a.QueueSnapshot().Active, "must execute a real sequential queue")
	}
	for i, id := range ids {
		payload, _ := json.Marshal(map[string]string{"session_id": id})
		result, rpcErr := s.handleSequentialQueue(ctx, payload)
		require.Nil(t, rpcErr)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		var snapshot struct {
			SessionID string `json:"session_id"`
			Active    bool   `json:"active"`
			Total     int    `json:"total"`
			Items     []struct {
				Description string `json:"description"`
			} `json:"items"`
		}
		require.NoError(t, json.Unmarshal(encoded, &snapshot))
		require.Equal(t, id, snapshot.SessionID)
		if i == 2 {
			require.False(t, snapshot.Active)
			require.Zero(t, snapshot.Total)
			require.Empty(t, snapshot.Items)
			require.NotContains(t, s.agents, id)
		} else {
			require.True(t, snapshot.Active)
			require.Equal(t, 3, snapshot.Total)
			for _, item := range snapshot.Items {
				require.Contains(t, item.Description, fmt.Sprintf("Queue %d", i))
				require.NotContains(t, item.Description, fmt.Sprintf("Queue %d", 1-i))
			}
		}
	}
	for _, payload := range []string{"", `null`, `{}`, `[]`, `{"session_id":42}`, `{"session_id":" "}`, `{"session_id":"../bad"}`, `{"session_id":"unknown-session"}`} {
		_, rpcErr := s.handleSequentialQueue(ctx, []byte(payload))
		require.NotNil(t, rpcErr, payload)
		require.Equal(t, InvalidParams, rpcErr.Code, payload)
	}
	require.Len(t, s.agents, 2, "inspection must not instantiate idle or unknown agents")
}
