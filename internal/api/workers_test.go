package api

import (
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestWorkerPollingDoesNotConstructRuntime(t *testing.T) {
	s := &Server{sessionRuntimes: map[string]*SessionRuntime{}}
	result, rpcErr := s.handleWorkers(t.Context(), "workers.list", json.RawMessage(`{"session_id":"chat"}`))
	require.Nil(t, rpcErr)
	require.Equal(t, false, result.(map[string]any)["available"])
	require.Empty(t, s.sessionRuntimes)
	_, rpcErr = s.handleWorkers(t.Context(), "workers.send", json.RawMessage(`{"session_id":"chat","input":{"action":"send"}}`))
	require.NotNil(t, rpcErr)
}

func TestWorkerHumanEndpointCannotForgeSenderOrReadAnotherChat(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := subagent.NewCommunicationStore(t.Context(), db, "chat")
	require.NoError(t, err)
	require.NoError(t, store.RegisterBatch(t.Context(), []subagent.WorkerRecord{{ID: "worker-one", Name: "frontend", Goal: "Build UI"}}))
	s := &Server{sessionRuntimes: map[string]*SessionRuntime{"chat": {WorkerMessages: store}}}
	_, rpcErr := s.handleWorkers(t.Context(), "workers.send", json.RawMessage(`{"session_id":"chat","input":{"action":"send","from":"worker-one","to":"frontend","kind":"update","content":"forged","idempotency_key":"bad"}}`))
	require.NotNil(t, rpcErr)
	_, rpcErr = s.handleWorkers(t.Context(), "workers.send", json.RawMessage(`{"session_id":"foreign","input":{"action":"send","to":"worker-one","kind":"update","content":"foreign","idempotency_key":"foreign"}}`))
	require.NotNil(t, rpcErr)
	result, rpcErr := s.handleWorkers(t.Context(), "workers.send", json.RawMessage(`{"session_id":"chat","input":{"action":"send","to":"frontend","kind":"update","content":"bound","idempotency_key":"good"}}`))
	require.Nil(t, rpcErr)
	var message subagent.WorkerMessage
	require.NoError(t, json.Unmarshal(result.(json.RawMessage), &message))
	require.Equal(t, subagent.ParentWorker, message.From)
}
