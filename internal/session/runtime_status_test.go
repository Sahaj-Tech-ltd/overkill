package session

import (
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestRuntimeStatusStoreRejectsUnownedInvalidAndOversizedRecords(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := NewPostgresStore(db)
	require.NoError(t, err)
	require.ErrorIs(t, store.SaveRuntimeStatus(t.Context(), "unknown", json.RawMessage(`{}`)), ErrNotFound)
	metadata := NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), metadata))
	require.Error(t, store.SaveRuntimeStatus(t.Context(), metadata.ID, json.RawMessage(`invalid`)))
	require.Error(t, store.SaveRuntimeStatus(t.Context(), metadata.ID, json.RawMessage(`"`+strings.Repeat("x", MaxRuntimeStatusBytes)+`"`)))
	metadata.Metadata["unrelated"] = "unchanged"
	require.NoError(t, store.Save(t.Context(), metadata))
	require.NoError(t, store.SaveRuntimeStatus(t.Context(), metadata.ID, json.RawMessage(`{"observed":true}`)))
	metadata.Title = "updated transcript"
	require.NoError(t, store.Save(t.Context(), metadata))
	record, err := store.LoadRuntimeStatus(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"observed":true}`, string(record))
	loaded, err := store.Load(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.Equal(t, "unchanged", loaded.Metadata["unrelated"])
}
