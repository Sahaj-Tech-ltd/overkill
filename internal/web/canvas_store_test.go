package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func newCanvasTestStore(t *testing.T) (*CanvasStore, *sql.DB) {
	t.Helper()
	db, _ := testpg.Open(t)
	_, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `INSERT INTO sessions(id) VALUES ('one'),('two')`)
	require.NoError(t, err)
	store := NewCanvasStore(db)
	require.NoError(t, store.Migrate(t.Context()))
	return store, db
}
func TestCanvasDurableStoreRestartIsolationAndRollback(t *testing.T) {
	store, db := newCanvasTestStore(t)
	state, err := store.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, int64(0), state.Revision)
	require.Empty(t, state.Surfaces)
	for _, line := range []string{
		`{"createSurface":{"surfaceId":"v","catalog":"overkill.canvas.v1"}}`,
		`{"surfaceUpdate":{"surfaceId":"v","components":[{"id":"root","type":"Text","textPath":"/title"}]}}`,
		`{"dataModelUpdate":{"surfaceId":"v","contents":{"title":"persisted","exact":9007199254740991}}}`,
		`{"beginRendering":{"surfaceId":"v","root":"root"}}`,
	} {
		state, err = store.Apply(t.Context(), "one", line)
		require.NoError(t, err)
	}
	reopened := NewCanvasStore(db)
	require.NoError(t, reopened.Migrate(t.Context()))
	actual, err := reopened.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, state, actual)
	require.Equal(t, json.Number("9007199254740991"), actual.Surfaces[0].Data["exact"])
	other, err := store.Get(t.Context(), "two")
	require.NoError(t, err)
	require.Equal(t, int64(0), other.Revision)
	require.Empty(t, other.Surfaces)
	for _, line := range []string{
		`{"surfaceUpdate":{"surfaceId":"v","components":[{"id":"other","type":"Divider"}]}}`,
		`{"dataModelUpdate":{"surfaceId":"v","contents":{"title":"replacement","title":"ambiguous"}}}`,
		`{"deleteSurface":{"surfaceId":"missing"}}`,
		`{"dataModelUpdate":{"surfaceId":"v","contents":{"exact":9007199254740993}}}`,
		`{"dataModelUpdate":{"surfaceId":"v","contents":{"exact":1e9999}}}`,
		`{"createSurface":{"surfaceId":"v","catalog":"overkill.canvas.v1"}}`,
	} {
		_, err = store.Apply(t.Context(), "one", line)
		require.ErrorIs(t, err, ErrCanvasInvalid)
		actual, err = store.Get(t.Context(), "one")
		require.NoError(t, err)
		require.Equal(t, state, actual)
	}
	reset, err := store.Reset(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, state.Revision+1, reset.Revision)
	require.Empty(t, reset.Surfaces)
	actual, err = reopened.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, reset, actual)
	_, err = db.ExecContext(t.Context(), `DELETE FROM sessions WHERE id='one'`)
	require.NoError(t, err)
	_, err = store.Get(t.Context(), "one")
	require.ErrorIs(t, err, ErrCanvasSessionNotFound)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM canvas_states WHERE session_id='one'`).Scan(&count))
	require.Zero(t, count)
}
func TestCanvasDurableConcurrentFirstWritesAndRevisions(t *testing.T) {
	store, db := newCanvasTestStore(t)
	// Exercise simultaneous first migration, not only an existing table's IF NOT EXISTS.
	_, err := db.ExecContext(t.Context(), `DROP TABLE canvas_states`)
	require.NoError(t, err)
	start := make(chan struct{})
	failures := make(chan error, 16)
	var workers sync.WaitGroup
	for i := range 16 {
		workers.Go(func() {
			<-start
			peer := NewCanvasStore(db)
			if err := peer.Migrate(t.Context()); err != nil {
				failures <- err
				return
			}
			_, err := peer.Apply(t.Context(), "one", canvasLine("createSurface", map[string]any{"surfaceId": fmt.Sprint(i), "catalog": CatalogCanvasV1}))
			failures <- err
		})
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	state, err := store.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, int64(16), state.Revision)
	require.Len(t, state.Surfaces, 16)
	failures = make(chan error, 16)
	for i := range 16 {
		workers.Go(func() {
			_, err := NewCanvasStore(db).Apply(t.Context(), "one", canvasLine("dataModelUpdate", map[string]any{"surfaceId": fmt.Sprint(i), "contents": map[string]any{"value": i}}))
			failures <- err
		})
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	state, err = store.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, int64(32), state.Revision)
	for _, surface := range state.Surfaces {
		require.Equal(t, json.Number(surface.SurfaceID), surface.Data["value"])
	}
}
func TestCanvasDurableMissingNilAndCancelled(t *testing.T) {
	store, db := newCanvasTestStore(t)
	line := `{"createSurface":{"surfaceId":"v","catalog":"overkill.canvas.v1"}}`
	_, err := store.Apply(t.Context(), "absent", line)
	require.ErrorIs(t, err, ErrCanvasSessionNotFound)
	_, err = store.Reset(t.Context(), "absent")
	require.ErrorIs(t, err, ErrCanvasSessionNotFound)
	_, err = store.Get(t.Context(), "absent")
	require.ErrorIs(t, err, ErrCanvasSessionNotFound)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM canvas_states`).Scan(&count))
	require.Zero(t, count)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = store.Apply(ctx, "one", line)
	require.Error(t, err)
	state, err := store.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Zero(t, state.Revision)
	for _, empty := range []*CanvasStore{nil, NewCanvasStore(nil)} {
		require.ErrorIs(t, empty.Migrate(t.Context()), ErrCanvasUnavailable)
		_, err = empty.Get(t.Context(), "one")
		require.ErrorIs(t, err, ErrCanvasUnavailable)
		_, err = empty.Apply(t.Context(), "one", line)
		require.ErrorIs(t, err, ErrCanvasUnavailable)
		_, err = empty.Reset(t.Context(), "one")
		require.ErrorIs(t, err, ErrCanvasUnavailable)
	}
}
func TestCanvasDurableStateSizeFailureConsumesNoRevision(t *testing.T) {
	store, _ := newCanvasTestStore(t)
	for i := range 5 {
		_, err := store.Apply(t.Context(), "one", canvasLine("createSurface", map[string]any{"surfaceId": fmt.Sprint(i), "catalog": CatalogCanvasV1}))
		require.NoError(t, err)
	}
	for i := range 4 {
		_, err := store.Apply(t.Context(), "one", canvasLine("dataModelUpdate", map[string]any{"surfaceId": fmt.Sprint(i), "contents": map[string]any{"unused": strings.Repeat("x", 64000)}}))
		require.NoError(t, err)
	}
	before, err := store.Get(t.Context(), "one")
	require.NoError(t, err)
	_, err = store.Apply(t.Context(), "one", canvasLine("dataModelUpdate", map[string]any{"surfaceId": "4", "contents": map[string]any{"unused": strings.Repeat("x", 64000)}}))
	require.ErrorIs(t, err, ErrCanvasInvalid)
	after, err := store.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, before, after)
}
func TestCanvasDurableRoutesPublishCommittedRevisionAndRecoverState(t *testing.T) {
	store, _ := newCanvasTestStore(t)
	srv := NewServer(Config{Token: "secret", Canvas: store})
	sub := srv.bus.subscribe()
	defer srv.bus.unsubscribe(sub)
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		srv.Handler().ServeHTTP(out, req)
		return out
	}
	require.Equal(t, http.StatusUnauthorized, request("GET", "/api/canvas/state?sessionId=one", "", "").Code)
	for _, query := range []string{"", "?sessionId=one&sessionId=two", "?sessionId=one&unknown=x", "?sessionId=one&bad=%GG", "?sessionId=one;two"} {
		require.Equal(t, http.StatusBadRequest, request("GET", "/api/canvas/state"+query, "", "secret").Code)
	}
	require.Equal(t, http.StatusNotFound, request("GET", "/api/canvas/state?sessionId=absent", "", "secret").Code)
	body, _ := json.Marshal(map[string]string{"sessionId": "one", "jsonl": `{"createSurface":{"surfaceId":"v","catalog":"overkill.canvas.v1"}}`})
	out := request("POST", "/api/canvas/push", string(body), "secret")
	require.Equal(t, http.StatusAccepted, out.Code, out.Body.String())
	event := <-sub.ch
	var published CanvasEvent
	require.NoError(t, json.Unmarshal([]byte(event.Content), &published))
	require.Equal(t, int64(1), published.Revision)
	committed, err := store.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, published.Revision, committed.Revision)
	out = request("POST", "/api/canvas/push", string(body), "secret")
	require.Equal(t, http.StatusBadRequest, out.Code)
	select {
	case <-sub.ch:
		t.Fatal("invalid update was published")
	default:
	}
	out = request("GET", "/api/canvas/state?sessionId=one", "", "secret")
	require.Equal(t, http.StatusOK, out.Code)
	var recovered CanvasState
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &recovered))
	require.Equal(t, committed, recovered)
	out = request("POST", "/api/canvas/reset", `{"sessionId":"one"}`, "secret")
	require.Equal(t, http.StatusOK, out.Code)
	event = <-sub.ch
	require.Contains(t, event.Content, `"revision":2`)
	out = request("POST", "/api/canvas/reset", `{"sessionId":"absent"}`, "secret")
	require.Equal(t, http.StatusNotFound, out.Code)
}

func TestCanvasDurableRevisionBrowserSafeBoundary(t *testing.T) {
	store, db := newCanvasTestStore(t)
	state, err := store.Apply(t.Context(), "one", `{"createSurface":{"surfaceId":"v","catalog":"overkill.canvas.v1"}}`)
	require.NoError(t, err)
	state.Revision = CanvasMaxRevision - 1
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `UPDATE canvas_states SET revision=$2,state=$3 WHERE session_id=$1`, "one", state.Revision, string(raw))
	require.NoError(t, err)
	final, err := store.Apply(t.Context(), "one", `{"dataModelUpdate":{"surfaceId":"v","contents":{"retained":"at final safe revision"}}}`)
	require.NoError(t, err)
	require.Equal(t, CanvasMaxRevision, final.Revision)
	loaded, err := store.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, final, loaded)
	_, err = store.Apply(t.Context(), "one", `{"deleteSurface":{"surfaceId":"v"}}`)
	require.ErrorIs(t, err, ErrCanvasInvalid)
	_, err = store.Reset(t.Context(), "one")
	require.ErrorIs(t, err, ErrCanvasInvalid)
	loaded, err = store.Get(t.Context(), "one")
	require.NoError(t, err)
	require.Equal(t, final, loaded)
	// A direct operator write can bypass application validation. Such a row
	// must be refused on read instead of emitting an imprecise browser revision.
	final.Revision = CanvasMaxRevision + 1
	raw, err = json.Marshal(final)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `UPDATE canvas_states SET revision=$2,state=$3 WHERE session_id=$1`, "one", final.Revision, string(raw))
	require.NoError(t, err)
	_, err = store.Get(t.Context(), "one")
	require.ErrorContains(t, err, "identity mismatch")
	_, err = store.Reset(t.Context(), "one")
	require.ErrorContains(t, err, "identity mismatch")
}
