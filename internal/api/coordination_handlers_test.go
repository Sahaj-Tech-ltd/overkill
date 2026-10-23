package api

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestCoordinationApplicationControlsUseDurableIdentity(t *testing.T) {
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	coord, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	folder := t.TempDir()
	front, back := session.NewSession(folder), session.NewSession(folder)
	for _, metadata := range []*session.Session{front, back} {
		require.NoError(t, store.Create(t.Context(), metadata))
	}
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: store, CoordStore: coord})
	startHTTPServer(t, server)
	var cleanup atomic.Int32
	server.mu.Lock()
	server.agents[front.ID] = agent.New(agent.Config{Provider: streamingTestProvider{}, Tools: tools.NewRegistry(), Model: "fixture"})
	server.sessionRuntimes[front.ID] = &SessionRuntime{Cleanup: func() { cleanup.Add(1) }}
	server.mu.Unlock()
	registered := sidebarRPC(t, server, "coordination.register", map[string]string{"session_id": front.ID, "name": "frontend", "role": "Build frontend screens"})
	require.Nil(t, registered.Error)
	require.Equal(t, int32(1), cleanup.Load())
	server.mu.RLock()
	_, retained := server.agents[front.ID]
	server.mu.RUnlock()
	require.False(t, retained)
	registered = sidebarRPC(t, server, "coordination.register", map[string]string{"session_id": back.ID, "name": "backend", "role": "Build backend endpoints"})
	require.Nil(t, registered.Error)
	hold, err := server.beginSessionOperation(back.ID)
	require.NoError(t, err)
	defer hold()
	for _, invalid := range []any{map[string]any{"session_id": front.ID, "to": "backend", "kind": "request", "payload": "question", "idempotency_key": "same", "from": "intruder"}, "   "} {
		response := sidebarRPC(t, server, "coordination.send", invalid)
		require.NotNil(t, response.Error)
	}
	sent := sidebarRPC(t, server, "coordination.send", map[string]any{"session_id": front.ID, "to": "backend", "kind": "request", "payload": "Which endpoint is ready?", "idempotency_key": "same"})
	require.Nil(t, sent.Error)
	encoded, err := json.Marshal(sent.Result)
	require.NoError(t, err)
	var receipt struct {
		Message coordination.Message `json:"message"`
	}
	require.NoError(t, json.Unmarshal(encoded, &receipt))
	peer, err := coord.PeerForSession(t.Context(), front.ID)
	require.NoError(t, err)
	require.Equal(t, peer.ID, receipt.Message.From)
	listed := sidebarRPC(t, server, "coordination.messages", map[string]string{"session_id": front.ID})
	require.Nil(t, listed.Error)
	encoded, _ = json.Marshal(listed.Result)
	require.Contains(t, string(encoded), receipt.Message.ID)
	denied := sidebarRPC(t, server, "session.delete", map[string]string{"id": front.ID})
	require.NotNil(t, denied.Error)
	cancelled := sidebarRPC(t, server, "coordination.cancel", map[string]any{"session_id": front.ID, "message_id": receipt.Message.ID, "expected_revision": receipt.Message.Revision})
	require.Nil(t, cancelled.Error)
	message, err := coord.Read(t.Context(), peer.ID, receipt.Message.ID)
	require.NoError(t, err)
	require.Equal(t, coordination.Cancelled, message.State)
}

func TestCoordinationEventsReplayAcrossServerRestart(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	coord, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, CoordStore: coord})
	startHTTPServer(t, server)
	folder := t.TempDir()
	ids := make([]string, 3)
	peers := make([]coordination.Peer, 3)
	for i, name := range []string{"frontend", "backend", "reviewer"} {
		metadata := session.NewSession(folder)
		require.NoError(t, sessions.Create(t.Context(), metadata))
		ids[i] = metadata.ID
		peers[i], err = coord.RegisterSessionPeer(t.Context(), metadata.ID, name, "fixture")
		require.NoError(t, err)
	}
	hold, err := server.beginSessionOperation(ids[1])
	require.NoError(t, err)
	defer hold()
	message, err := coord.Send(t.Context(), peers[0].ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`"schema?"`), IdempotencyKey: "events-front"})
	require.NoError(t, err)
	_, err = coord.Send(t.Context(), peers[2].ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`"private review"`), IdempotencyKey: "events-review"})
	require.NoError(t, err)
	_, err = coord.Transition(t.Context(), peers[0].ID, message.ID, message.Revision, string(coordination.Cancelled), json.RawMessage(`{}`))
	require.NoError(t, err)
	read := func(target *Server, after int64, limit int) (events []coordination.Event, next int64, more bool) {
		t.Helper()
		response := sidebarRPC(t, target, "coordination.events", map[string]any{"session_id": ids[0], "after": after, "limit": limit})
		require.Nil(t, response.Error)
		data, err := json.Marshal(response.Result)
		require.NoError(t, err)
		var page struct {
			Events []coordination.Event `json:"events"`
			Next   int64                `json:"next_cursor"`
			More   bool                 `json:"has_more"`
		}
		require.NoError(t, json.Unmarshal(data, &page))
		return page.Events, page.Next, page.More
	}
	first, cursor, more := read(server, 0, 1)
	require.Len(t, first, 1)
	require.Equal(t, message.ID, first[0].MessageID)
	require.True(t, more)
	// A new application server resumes from committed database events.
	restarted := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, CoordStore: coord})
	startHTTPServer(t, restarted)
	holdRestart, err := restarted.beginSessionOperation(ids[1])
	require.NoError(t, err)
	defer holdRestart()
	replayed, next, more := read(restarted, cursor, 10)
	require.Len(t, replayed, 1)
	require.Equal(t, "cancelled", replayed[0].Type)
	require.Greater(t, next, cursor)
	require.False(t, more)
	empty, unchanged, more := read(restarted, next, 10)
	require.Empty(t, empty)
	require.Equal(t, next, unchanged)
	require.False(t, more)
	// An offline client can wait for the next event without claiming a task.
	params, err := json.Marshal(map[string]any{"session_id": ids[0], "after": next, "wait_ms": 1000})
	require.NoError(t, err)
	type pollResult struct {
		value any
		err   *RPCError
	}
	result := make(chan pollResult, 1)
	go func() {
		value, rpcError := server.handleCoordination(t.Context(), "coordination.events", params)
		result <- pollResult{value, rpcError}
	}()
	followup, err := coord.Send(t.Context(), peers[0].ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`"next schema?"`), IdempotencyKey: "events-followup"})
	require.NoError(t, err)
	select {
	case polled := <-result:
		require.Nil(t, polled.err)
		data, err := json.Marshal(polled.value)
		require.NoError(t, err)
		require.Contains(t, string(data), followup.ID)
	case <-time.After(2 * time.Second):
		t.Fatal("event waiter did not return new committed event")
	}
	readBack, err := coord.Read(t.Context(), peers[0].ID, followup.ID)
	require.NoError(t, err)
	require.Equal(t, coordination.Accepted, readBack.State)
	for _, invalid := range []map[string]any{{"after": -1}, {"limit": 201}, {"wait_ms": 25001}, {"from": "backend"}} {
		invalid["session_id"] = ids[0]
		require.NotNil(t, sidebarRPC(t, server, "coordination.events", invalid).Error)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	params, err = json.Marshal(map[string]any{"session_id": ids[0], "after": next, "wait_ms": 25000})
	require.NoError(t, err)
	start := time.Now()
	_, rpcError := server.handleCoordination(ctx, "coordination.events", params)
	require.NotNil(t, rpcError)
	require.Less(t, time.Since(start), time.Second)
}

func TestCoordinationMessageInspectionReplaysOnlyAuthorizedTask(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	coord, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, CoordStore: coord})
	metadata := []*session.Session{session.NewSession(t.TempDir()), session.NewSession(t.TempDir()), session.NewSession(t.TempDir())}
	peers := make([]coordination.Peer, 3)
	for i, name := range []string{"frontend", "backend", "unrelated"} {
		require.NoError(t, sessions.Create(t.Context(), metadata[i]))
		peers[i], _, err = coord.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "inspection", Name: name, Role: name, Workspace: metadata[i].Folder})
		require.NoError(t, err)
		require.NoError(t, coord.BindSession(t.Context(), peers[i].ID, metadata[i].ID))
	}
	hold, err := server.beginSessionOperation(metadata[1].ID)
	require.NoError(t, err)
	defer hold()
	startHTTPServer(t, server)
	message, err := coord.Send(t.Context(), peers[0].ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`"inspect me"`), IdempotencyKey: "inspect"})
	require.NoError(t, err)
	_, err = coord.Transition(t.Context(), peers[0].ID, message.ID, message.Revision, "cancelled", json.RawMessage(`{"reason":"user_cancelled"}`))
	require.NoError(t, err)
	inspect := func(after int64) struct {
		Message coordination.Message `json:"message"`
		Events  []coordination.Event `json:"events"`
		Next    int64                `json:"next_revision"`
		More    bool                 `json:"has_more"`
	} {
		response := sidebarRPC(t, server, "coordination.message", map[string]any{"session_id": metadata[0].ID, "message_id": message.ID, "after_revision": after, "limit": 1})
		require.Nil(t, response.Error)
		data, err := json.Marshal(response.Result)
		require.NoError(t, err)
		var page struct {
			Message coordination.Message `json:"message"`
			Events  []coordination.Event `json:"events"`
			Next    int64                `json:"next_revision"`
			More    bool                 `json:"has_more"`
		}
		require.NoError(t, json.Unmarshal(data, &page))
		return page
	}
	first := inspect(0)
	require.Equal(t, coordination.Cancelled, first.Message.State)
	require.Len(t, first.Events, 1)
	require.True(t, first.More)
	require.Equal(t, "accepted", first.Events[0].Type)
	second := inspect(first.Next)
	require.Len(t, second.Events, 1)
	require.False(t, second.More)
	require.Equal(t, "cancelled", second.Events[0].Type)
	for _, input := range []map[string]any{{"session_id": metadata[2].ID, "message_id": message.ID}, {"session_id": metadata[0].ID, "message_id": message.ID, "from": peers[1].ID}, {"session_id": metadata[0].ID, "message_id": message.ID, "after_revision": -1}} {
		response := sidebarRPC(t, server, "coordination.message", input)
		require.NotNil(t, response.Error)
	}
}

func TestCoordinationDependencyGraphUsesSessionIdentity(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	coord, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, CoordStore: coord})
	startHTTPServer(t, server)
	folder := t.TempDir()
	ids := make([]string, 3)
	peers := make([]coordination.Peer, 3)
	for i, name := range []string{"frontend", "backend", "reviewer"} {
		metadata := session.NewSession(folder)
		require.NoError(t, sessions.Create(t.Context(), metadata))
		ids[i] = metadata.ID
		peers[i], err = coord.RegisterSessionPeer(t.Context(), metadata.ID, name, "fixture")
		require.NoError(t, err)
		release, err := server.beginSessionOperation(metadata.ID)
		require.NoError(t, err)
		defer release()
	}
	first, err := coord.Send(t.Context(), peers[0].ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`"private schema details"`), IdempotencyKey: "graph-first"})
	require.NoError(t, err)
	sent := sidebarRPC(t, server, "coordination.send", map[string]any{"session_id": ids[0], "to": "reviewer", "kind": "request", "payload": "integrate schema", "idempotency_key": "graph-second", "depends_on": []string{first.ID}})
	require.Nil(t, sent.Error)
	data, _ := json.Marshal(sent.Result)
	var receipt struct {
		Message coordination.Message `json:"message"`
	}
	require.NoError(t, json.Unmarshal(data, &receipt))
	require.Equal(t, []string{first.ID}, receipt.Message.DependsOn)
	graphResponse := sidebarRPC(t, server, "coordination.graph", map[string]any{"session_id": ids[0], "message_id": receipt.Message.ID})
	require.Nil(t, graphResponse.Error)
	data, _ = json.Marshal(graphResponse.Result)
	var graph coordination.DependencyGraph
	require.NoError(t, json.Unmarshal(data, &graph))
	require.Len(t, graph.Nodes, 2)
	require.Equal(t, "waiting", graph.Nodes[0].Readiness)
	privateResponse := sidebarRPC(t, server, "coordination.graph", map[string]any{"session_id": ids[2], "message_id": receipt.Message.ID})
	require.Nil(t, privateResponse.Error)
	data, _ = json.Marshal(privateResponse.Result)
	require.NotContains(t, string(data), "private schema details")
	require.NoError(t, json.Unmarshal(data, &graph))
	require.Len(t, graph.Nodes, 1)
	require.True(t, graph.HiddenDependencies)
	denied := sidebarRPC(t, server, "coordination.graph", map[string]any{"session_id": ids[2], "message_id": first.ID})
	require.NotNil(t, denied.Error)
	for _, input := range []map[string]any{{"session_id": ids[0], "message_id": receipt.Message.ID, "from": peers[0].ID}, {"session_id": ids[0], "message_id": receipt.Message.ID, "limit": 201}} {
		require.NotNil(t, sidebarRPC(t, server, "coordination.graph", input).Error)
	}
}
