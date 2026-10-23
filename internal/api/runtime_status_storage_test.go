package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
	"testing"
)

type failingRuntimeStatusStore struct{ session.Store }

func (failingRuntimeStatusStore) LoadRuntimeStatus(context.Context, string) (json.RawMessage, error) {
	return nil, errors.New("controlled unavailable database")
}
func (failingRuntimeStatusStore) SaveRuntimeStatus(context.Context, string, json.RawMessage) error {
	return errors.New("controlled unavailable database")
}
func TestRuntimeStatusUnavailableAndForeignSnapshotsNeverBecomeEmptySuccess(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	metadata := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), metadata))
	params, _ := json.Marshal(map[string]string{"session_id": metadata.ID})
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: failingRuntimeStatusStore{store}})
	for _, read := range []func(context.Context, []byte) (interface{}, *RPCError){server.testResults, server.selfEvalStatus} {
		result, rpcErr := read(t.Context(), params)
		require.Nil(t, result)
		require.NotNil(t, rpcErr)
		require.Equal(t, InternalError, rpcErr.Code)
	}
	require.NoError(t, store.SaveRuntimeStatus(t.Context(), metadata.ID, json.RawMessage(`{"version":1,"session_id":"another-session","verification":{"source":"post_write_verifier","tests":[]}}`)))
	server = NewServer(ServerConfig{Config: config.Default(), SessionStore: store})
	result, rpcErr := server.testResults(t.Context(), params)
	require.Nil(t, result)
	require.NotNil(t, rpcErr)
	require.Equal(t, InternalError, rpcErr.Code)
}
