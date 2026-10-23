package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func snapshotFixture(t *testing.T) (*Server, *CanvasStore, *sql.DB, string) {
	t.Helper()
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	sess := session.NewSession(t.TempDir())
	require.NoError(t, sessions.Create(t.Context(), sess))
	store := NewCanvasStore(database)
	require.NoError(t, store.Migrate(t.Context()))
	srv := NewServer(Config{Token: "snapshot-fixture", Canvas: store, Store: sessions})
	return srv, store, database, sess.ID
}
func snapshotMutation(t *testing.T, store *CanvasStore, sid, action string, payload map[string]any) CanvasState {
	t.Helper()
	payload["surfaceId"] = "view"
	raw, err := json.Marshal(map[string]any{action: payload})
	require.NoError(t, err)
	state, err := store.Apply(t.Context(), sid, string(raw))
	require.NoError(t, err)
	return state
}
func snapshotScene(t *testing.T, store *CanvasStore, sid string) {
	snapshotMutation(t, store, sid, "createSurface", map[string]any{"catalog": CatalogCanvasV1})
	snapshotMutation(t, store, sid, "surfaceUpdate", map[string]any{"components": []any{
		map[string]any{"id": "root", "type": "Column", "children": []string{"heading", "status"}},
		map[string]any{"id": "heading", "type": "Heading", "text": "Agent workspace"},
		map[string]any{"id": "status", "type": "Text", "textPath": "/status"},
	}})
	snapshotMutation(t, store, sid, "dataModelUpdate", map[string]any{"contents": map[string]any{"status": "Backend ready"}})
	snapshotMutation(t, store, sid, "beginRendering", map[string]any{"root": "root"})
}
func snapshotHTTP(srv *Server, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/canvas/snapshot?"+query, nil)
	req.Header.Set("Authorization", "Bearer snapshot-fixture")
	out := httptest.NewRecorder()
	srv.Handler().ServeHTTP(out, req)
	return out
}
func snapshotChrome(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("OVERKILL_REQUIRE_FRONTEND_TEST") == "1" {
			t.Fatal(err)
		}
		t.Skip("Node unavailable for browser discovery")
	}
	output, err := exec.Command(node, "-p", `require('../../desktop/node_modules/playwright').chromium.executablePath()`).CombinedOutput()
	if err != nil {
		if os.Getenv("OVERKILL_REQUIRE_FRONTEND_TEST") == "1" {
			t.Fatalf("Chromium discovery: %v %s", err, output)
		}
		t.Skip("Playwright browser unavailable")
	}
	path := strings.TrimSpace(string(output))
	_, err = os.Stat(path)
	if err != nil {
		if os.Getenv("OVERKILL_REQUIRE_FRONTEND_TEST") == "1" {
			t.Fatal(err)
		}
		t.Skip("Chromium not installed")
	}
	return path
}
func TestCanvasSnapshotQueriesStateAndUnavailableRuntime(t *testing.T) {
	srv, store, _, sid := snapshotFixture(t)
	for _, q := range []string{"", "sessionId=" + sid, "sessionId=" + sid + "&surfaceId=view&unknown=1", "sessionId=" + sid + "&surfaceId=view&width=0", "sessionId=" + sid + "&surfaceId=view&height=99999", "sessionId=" + sid + "&surfaceId=view&width=640&width=640", "sessionId=" + sid + "&surfaceId=view&width=%2B640", "sessionId=" + sid + "&surfaceId=view&x=%zz"} {
		out := snapshotHTTP(srv, q)
		require.Equal(t, 400, out.Code, q)
	}
	require.Equal(t, 404, snapshotHTTP(srv, "sessionId=unknown&surfaceId=view").Code)
	require.Equal(t, 404, snapshotHTTP(srv, "sessionId="+sid+"&surfaceId=view").Code)
	snapshotMutation(t, store, sid, "createSurface", map[string]any{"catalog": CatalogCanvasV1})
	require.Equal(t, 409, snapshotHTTP(srv, "sessionId="+sid+"&surfaceId=view").Code)
	snapshotMutation(t, store, sid, "surfaceUpdate", map[string]any{"components": []any{map[string]any{"id": "root", "type": "Text", "text": "visible"}}})
	snapshotMutation(t, store, sid, "beginRendering", map[string]any{"root": "root"})
	srv.cfg.CanvasChromePath = filepath.Join(t.TempDir(), "missing-chromium")
	require.Equal(t, 503, snapshotHTTP(srv, "sessionId="+sid+"&surfaceId=view").Code)
	srv.canvasSnapshotSlots <- struct{}{}
	srv.canvasSnapshotSlots <- struct{}{}
	require.Equal(t, 429, snapshotHTTP(srv, "sessionId="+sid+"&surfaceId=view").Code)
	<-srv.canvasSnapshotSlots
	<-srv.canvasSnapshotSlots
}
func TestCanvasSnapshotActualPNGAndFrozenConcurrentRevision(t *testing.T) {
	srv, store, database, sid := snapshotFixture(t)
	srv.cfg.CanvasChromePath = snapshotChrome(t)
	snapshotScene(t, store, sid)
	query := "sessionId=" + sid + "&surfaceId=view&width=640&height=360"
	first := snapshotHTTP(srv, query)
	require.Equal(t, 200, first.Code, first.Body.String())
	require.Equal(t, "image/png", first.Header().Get("Content-Type"))
	require.Equal(t, "4", first.Header().Get("X-Canvas-Revision"))
	image, err := png.Decode(bytes.NewReader(first.Body.Bytes()))
	require.NoError(t, err)
	require.Equal(t, 640, image.Bounds().Dx())
	require.Equal(t, 360, image.Bounds().Dy())
	snapshotMutation(t, store, sid, "dataModelUpdate", map[string]any{"contents": map[string]any{"status": "Frontend integrated"}})
	second := snapshotHTTP(srv, query)
	require.Equal(t, 200, second.Code)
	_, err = png.Decode(bytes.NewReader(second.Body.Bytes()))
	require.NoError(t, err)
	require.NotEqual(t, first.Body.Bytes(), second.Body.Bytes(), "actual rendered text must change PNG")
	// Get returns decoded immutable state; reconstruction over the same database
	// retains the exact revision and rendering document.
	restarted := NewCanvasStore(database)
	require.NoError(t, restarted.Migrate(t.Context()))
	state, err := restarted.Get(t.Context(), sid)
	require.NoError(t, err)
	require.Equal(t, int64(5), state.Revision)
	// Update while Chromium captures. The returned revision must describe one
	// frozen document, never a mixture or a revision read after capture.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	failures := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		revision := int64(5)
		for {
			select {
			case <-stop:
				return
			default:
			}
			value := "Backend ready"
			if (revision+1)%2 == 1 {
				value = "Frontend integrated"
			}
			raw, _ := json.Marshal(map[string]any{"dataModelUpdate": map[string]any{"surfaceId": "view", "contents": map[string]any{"status": value}}})
			next, err := store.Apply(context.Background(), sid, string(raw))
			if err != nil {
				failures <- err
				return
			}
			revision = next.Revision
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	captured := snapshotHTTP(srv, query)
	close(stop)
	wg.Wait()
	select {
	case err := <-failures:
		require.NoError(t, err)
	default:
	}
	require.Equal(t, 200, captured.Code, captured.Body.String())
	revision, err := strconv.ParseInt(captured.Header().Get("X-Canvas-Revision"), 10, 64)
	require.NoError(t, err)
	expected := first.Body.Bytes()
	if revision%2 == 1 {
		expected = second.Body.Bytes()
	}
	require.Equal(t, expected, captured.Body.Bytes(), "PNG must match the document identified by captured revision")
	latest, err := store.Get(t.Context(), sid)
	require.NoError(t, err)
	require.Greater(t, latest.Revision, revision, "writer must progress during capture")
	proof := filepath.Join("..", "..", ".cache", "canvas-snapshot-proof.png")
	require.NoError(t, os.MkdirAll(filepath.Dir(proof), 0700))
	require.NoError(t, os.WriteFile(proof, second.Body.Bytes(), 0600))
}

func TestCanvasActualDatabaseBrowserAndSnapshot(t *testing.T) {
	srv, store, database, sid := snapshotFixture(t)
	srv.cfg.CanvasChromePath = snapshotChrome(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	primary, err := sessions.Load(t.Context(), sid)
	require.NoError(t, err)
	primary.Title = "Canvas primary"
	require.NoError(t, sessions.Save(t.Context(), primary))
	second := session.NewSession(t.TempDir())
	second.Title = "Canvas secondary"
	require.NoError(t, sessions.Create(t.Context(), second))
	snapshotScene(t, store, sid)
	snapshotScene(t, store, second.ID)
	snapshotMutation(t, store, second.ID, "dataModelUpdate", map[string]any{"contents": map[string]any{"status": "Other session"}})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "testdata/canvas-e2e.cjs")
	command.Env = append(os.Environ(), "OVERKILL_WEB_TEST_URL="+server.URL, "OVERKILL_WEB_TEST_TOKEN=snapshot-fixture", "OVERKILL_CANVAS_SESSION="+sid)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "Actual PostgreSQL canvas, browser DOM, WebSocket updates, PNG, invalid rollback, isolation, reload, delete and reset passed")
	final, err := NewCanvasStore(database).Get(t.Context(), sid)
	require.NoError(t, err)
	require.Equal(t, int64(7), final.Revision)
	require.Empty(t, final.Surfaces)
	untouched, err := store.Get(t.Context(), second.ID)
	require.NoError(t, err)
	require.Equal(t, int64(5), untouched.Revision)
	require.Len(t, untouched.Surfaces, 1)
}
