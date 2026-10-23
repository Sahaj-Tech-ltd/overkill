package web

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanvasAcceptedNestedDataRemainsReadable(t *testing.T) {
	_, store, _, sid := snapshotFixture(t)
	snapshotMutation(t, store, sid, "createSurface", map[string]any{"catalog": CatalogCanvasV1})
	accepted := 0
	rejected := 0
	previous, err := store.Get(t.Context(), sid)
	require.NoError(t, err)
	for depth := 56; depth <= 66; depth++ {
		var value any = "leaf"
		for range depth {
			value = map[string]any{"nested": value}
		}
		raw, err := json.Marshal(map[string]any{"dataModelUpdate": map[string]any{"surfaceId": "view", "contents": value}})
		require.NoError(t, err)
		next, err := store.Apply(t.Context(), sid, string(raw))
		if err != nil {
			require.ErrorIs(t, err, ErrCanvasInvalid)
			rejected++
			loaded, loadErr := store.Get(t.Context(), sid)
			require.NoError(t, loadErr, "rejected depth %d must preserve readable previous state", depth)
			require.Equal(t, previous, loaded, "rejected depth %d must consume no revision or data", depth)
			continue
		}
		accepted++
		loaded, err := store.Get(t.Context(), sid)
		require.NoError(t, err, "accepted nesting depth %d must remain decodable", depth)
		require.Equal(t, next.Revision, loaded.Revision)
		previous = loaded
	}
	require.Positive(t, accepted)
	require.Positive(t, rejected)
}
