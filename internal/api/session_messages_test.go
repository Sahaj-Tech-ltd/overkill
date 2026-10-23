package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
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

func TestSessionMessagesReadsPersistedStreamFromFreshServer(t *testing.T) {
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	metadata := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), metadata))
	s := NewServer(ServerConfig{Config: config.Default(), SessionStore: store})
	a := agent.New(agent.Config{Provider: streamingTestProvider{}, Tools: tools.NewRegistry(), Model: "fixture", SessionID: metadata.ID})
	a.RestoreHistory([]providers.Message{{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "saved-tool", Name: "fs", Arguments: `{"action":"read","path":"README.md"}`}}}, {Role: "tool", ToolCallID: "saved-tool", Content: "saved output"}})
	s.agents[metadata.ID] = a
	startHTTPServer(t, s)
	client := &http.Client{Timeout: 5 * time.Second}
	request, err := http.NewRequest(http.MethodGet, s.Addr()+"/stream?session_id="+metadata.ID+"&message=remember", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+s.AuthToken())
	response, err := client.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Contains(t, string(body), "event: done")
	// A new service with an empty agent cache must read the durable transcript.
	fresh := NewServer(ServerConfig{Config: config.Default(), SessionStore: store})
	startHTTPServer(t, fresh)
	rpc := `{"jsonrpc":"2.0","id":1,"method":"session.messages","params":{"id":"` + metadata.ID + `"}}`
	request, err = http.NewRequest(http.MethodPost, fresh.Addr()+"/rpc", strings.NewReader(rpc))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+fresh.AuthToken())
	response, err = client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	var decoded struct {
		Result SessionMessagesResult
		Error  *RPCError
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&decoded))
	require.Nil(t, decoded.Error)
	require.Equal(t, metadata.ID, decoded.Result.ID)
	encoded, err := json.Marshal(decoded.Result.Messages)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "saved-tool")
	require.Contains(t, string(encoded), "saved output")
	require.Contains(t, string(encoded), "remember")
	require.Contains(t, string(encoded), "hello")
	require.Empty(t, fresh.agents)
}

func TestSessionMessagesAdmissionAndEmptyHistory(t *testing.T) {
	metadata := session.NewSession(t.TempDir())
	s := NewServer(ServerConfig{Config: config.Default(), SessionStore: &mockSessionStore{sessions: map[string]*session.Session{metadata.ID: metadata}}})
	defer s.Close()
	payload, _ := json.Marshal(map[string]string{"id": metadata.ID})
	release, err := s.beginSessionOperation(metadata.ID)
	require.NoError(t, err)
	_, rpcErr := s.handleSessionMessages(context.Background(), payload)
	require.NotNil(t, rpcErr)
	require.Equal(t, InvalidRequest, rpcErr.Code)
	release()
	result, rpcErr := s.handleSessionMessages(context.Background(), payload)
	require.Nil(t, rpcErr)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"messages":[]`)
}

func TestSessionMessagesCommittedSnapshotDuringTurnAndUncommittedWrite(t *testing.T) {
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	metadata := session.NewSession(t.TempDir())
	metadata.Messages = []providers.Message{{Role: "assistant", Content: "previous committed answer"}}
	require.NoError(t, store.Create(t.Context(), metadata))
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: store})
	startHTTPServer(t, server)
	release, err := server.beginSessionOperation(metadata.ID)
	require.NoError(t, err)
	defer release()
	tx, err := database.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `UPDATE sessions SET messages=$1::jsonb WHERE id=$2`, `[{"role":"assistant","content":"new committed answer"}]`, metadata.ID)
	require.NoError(t, err)
	// A held row update plus held turn admission must not block the committed
	// transcript read, nor expose the uncommitted replacement.
	read := func() *SessionMessagesResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]string{"id": metadata.ID})
		result, rpcErr := server.handleSessionMessages(ctx, payload)
		require.Nil(t, rpcErr)
		return result.(*SessionMessagesResult)
	}
	before := read()
	require.Equal(t, "committed", before.Snapshot)
	require.Equal(t, "previous committed answer", before.Messages[0].Content)
	before.Messages[0].Content = "reader mutation"
	require.Equal(t, "previous committed answer", read().Messages[0].Content)
	require.NoError(t, tx.Commit())
	require.Equal(t, "new committed answer", read().Messages[0].Content)
	// Reading never releases or replaces the in-flight writer's admission.
	_, err = server.beginSessionOperation(metadata.ID)
	require.Error(t, err)
	require.NoError(t, store.Delete(t.Context(), metadata.ID))
	payload, _ := json.Marshal(map[string]string{"id": metadata.ID})
	_, rpcErr := server.handleSessionMessages(t.Context(), payload)
	require.NotNil(t, rpcErr)
	require.Equal(t, InvalidParams, rpcErr.Code)
}
