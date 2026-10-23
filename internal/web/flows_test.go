package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

type managedFlowFixture struct {
	db         *sql.DB
	sessions   *session.PostgresStore
	server     *Server
	http       *httptest.Server
	sid, other string
}

func newManagedFlowFixture(t *testing.T) *managedFlowFixture {
	t.Helper()
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	first, other := session.NewSession(t.TempDir()), session.NewSession(t.TempDir())
	require.NoError(t, sessions.Create(t.Context(), first))
	require.NoError(t, sessions.Create(t.Context(), other))
	store := NewFlowStore(database)
	require.NoError(t, store.Migrate(t.Context()))
	require.NoError(t, store.Migrate(t.Context()))
	server := NewServer(Config{Token: "flow-test-token", Store: sessions, Flows: store})
	hs := httptest.NewServer(server.Handler())
	t.Cleanup(hs.Close)
	return &managedFlowFixture{database, sessions, server, hs, first.ID, other.ID}
}

func flowHTTP(t *testing.T, address, method, path, body, token string, status int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, address+path, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, status, resp.StatusCode, string(raw))
	return raw
}

func createManagedFlow(t *testing.T, f *managedFlowFixture, goal string) flowRow {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"sessionKey": f.sid, "goal": goal, "controllerId": "frontend", "stateJson": `{"tasks":["backend","frontend"]}`})
	raw := flowHTTP(t, f.http.URL, "POST", "/api/flows", string(body), "flow-test-token", 201)
	var row flowRow
	row = flowRow{}
	require.NoError(t, json.Unmarshal(raw, &row))
	require.EqualValues(t, 1, row.Revision)
	require.Equal(t, "running", row.Status)
	return row
}

func TestManagedFlowsPersistAndScopeHTTPActions(t *testing.T) {
	f := newManagedFlowFixture(t)
	sub := f.server.bus.subscribe()
	defer f.server.bus.unsubscribe(sub)
	row := createManagedFlow(t, f, "Build the users endpoint")
	first := <-sub.ch
	require.Equal(t, "flow_updated", first.Type)
	require.Equal(t, f.sid, first.SessionID)
	path := "/api/flows/" + row.ID + "?sessionKey=" + f.sid
	flowHTTP(t, f.http.URL, "GET", "/api/flows/"+row.ID+"?sessionKey="+f.other, "", "flow-test-token", 404)
	raw := flowHTTP(t, f.http.URL, "GET", "/api/flows?sessionKey="+f.other, "", "flow-test-token", 200)
	require.JSONEq(t, `{"flows":[],"hasMore":false,"nextOffset":null}`, string(raw))
	flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/cancel?sessionKey="+f.other, `{"expectedRevision":1}`, "flow-test-token", 404)
	flowHTTP(t, f.http.URL, "GET", path, "", "", 401)
	raw = flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/tasks?sessionKey="+f.sid, `{"expectedRevision":1,"status":"blocked","blockedTaskId":"backend","blockedSummary":"Schema needed","waitJson":"{\"contract\":1}","currentStep":"wire API"}`, "flow-test-token", 200)
	row = flowRow{}
	require.NoError(t, json.Unmarshal(raw, &row))
	require.EqualValues(t, 2, row.Revision)
	require.Equal(t, "blocked", row.Status)
	flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/resume?sessionKey="+f.sid, `{"expectedRevision":1}`, "flow-test-token", 409)
	// A new server/store instance reads committed records, not an in-memory map.
	f.http.Close()
	f.server = NewServer(Config{Token: "flow-test-token", Store: f.sessions, Flows: NewFlowStore(f.db)})
	f.http = httptest.NewServer(f.server.Handler())
	t.Cleanup(f.http.Close)
	raw = flowHTTP(t, f.http.URL, "GET", path, "", "flow-test-token", 200)
	row = flowRow{}
	require.NoError(t, json.Unmarshal(raw, &row))
	require.EqualValues(t, 2, row.Revision)
	require.Equal(t, "Schema needed", row.BlockedSummary)
	raw = flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/resume?sessionKey="+f.sid, `{"expectedRevision":2}`, "flow-test-token", 200)
	row = flowRow{}
	require.NoError(t, json.Unmarshal(raw, &row))
	require.EqualValues(t, 3, row.Revision)
	require.Equal(t, "running", row.Status)
	require.Empty(t, row.BlockedTaskID)
	require.JSONEq(t, "{}", row.WaitJSON)
	for _, action := range []string{"finish", "cancel"} {
		code := 200
		if action == "cancel" {
			code = 409
		}
		raw = flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/"+action+"?sessionKey="+f.sid, `{"expectedRevision":3}`, "flow-test-token", code)
	}
	var stored flowRow
	raw = flowHTTP(t, f.http.URL, "GET", path, "", "flow-test-token", 200)
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.Equal(t, "finished", stored.Status)
	require.EqualValues(t, 4, stored.Revision)
	require.NoError(t, f.sessions.Delete(context.Background(), f.sid))
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM web_task_flows WHERE session_key=$1`, f.sid).Scan(&count))
	require.Zero(t, count)
}

func TestManagedFlowsRejectAmbiguityWithoutConsumingRevision(t *testing.T) {
	f := newManagedFlowFixture(t)
	row := createManagedFlow(t, f, "test")
	for _, body := range []string{`{}`, `{"expectedRevision":0}`, `{"expectedRevision":1} {}`, `{"expectedRevision":1,"expectedRevision":2}`, `{"expectedRevision":1,"ExpectedRevision":2}`, `{"expectedRevision":1,"currentStep":null}`, `{"expectedRevision":1,"stateJson":"not-json"}`, `{"expectedRevision":1,"status":"finished"}`, `{"expectedRevision":1,"unknown":true}`} {
		flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/tasks?sessionKey="+f.sid, body, "flow-test-token", 400)
	}
	flowHTTP(t, f.http.URL, "GET", "/api/flows?sessionKey="+f.sid+"&sessionKey="+f.other, "", "flow-test-token", 400)
	flowHTTP(t, f.http.URL, "GET", "/api/flows?sessionKey="+f.sid+"&limit=201", "", "flow-test-token", 400)
	flowHTTP(t, f.http.URL, "GET", "/api/flows?sessionKey=missing", "", "flow-test-token", 404)
	flowHTTP(t, f.http.URL, "GET", "/api/flows?sessionKey="+f.sid+"&offset=-1", "", "flow-test-token", 400)
	flowHTTP(t, f.http.URL, "GET", "/api/flows?sessionKey="+f.sid+"&offset=9223372036854775807", "", "flow-test-token", 400)
	flowHTTP(t, f.http.URL, "GET", "/api/flows?sessionKey="+f.sid+"&offset=1000100", "", "flow-test-token", 200)
	flowHTTP(t, f.http.URL, "GET", "/api/flows/"+row.ID+"/tasks/extra?sessionKey="+f.sid, "", "flow-test-token", 404)
	flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"?sessionKey="+f.sid, `{}`, "flow-test-token", 405)
	flowHTTP(t, f.http.URL, "POST", "/api/flows", `{"sessionKey":"`+f.sid+`","goal":"x","goal":"y"}`, "flow-test-token", 400)
	flowHTTP(t, f.http.URL, "POST", "/api/flows", `{"sessionKey":"`+f.sid+`","goal":" "}`, "flow-test-token", 400)
	flowHTTP(t, f.http.URL, "POST", "/api/flows", `{"sessionKey":"`+f.sid+`","goal":"x","stateJson":"no"}`, "flow-test-token", 400)
	got, err := f.server.cfg.Flows.get(t.Context(), f.sid, row.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, got.Revision)
}

func TestManagedFlowsConcurrentRevisionAndPagination(t *testing.T) {
	f := newManagedFlowFixture(t)
	row := createManagedFlow(t, f, "first")
	createManagedFlow(t, f, "second")
	raw := flowHTTP(t, f.http.URL, "GET", "/api/flows?sessionKey="+f.sid+"&limit=1", "", "flow-test-token", 200)
	var page struct {
		Flows      []flowRow
		HasMore    bool
		NextOffset *int
	}
	require.NoError(t, json.Unmarshal(raw, &page))
	require.True(t, page.HasMore)
	require.Equal(t, 1, *page.NextOffset)
	require.Len(t, page.Flows, 1)
	raw = flowHTTP(t, f.http.URL, "GET", "/api/flows?sessionKey="+f.sid+"&limit=1&offset=1", "", "flow-test-token", 200)
	require.NoError(t, json.Unmarshal(raw, &page))
	require.False(t, page.HasMore)
	require.Nil(t, page.NextOffset)
	require.Equal(t, row.ID, page.Flows[0].ID)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			req, err := http.NewRequest("POST", f.http.URL+"/api/flows/"+row.ID+"/tasks?sessionKey="+f.sid, strings.NewReader(`{"expectedRevision":1,"currentStep":"parallel"}`))
			if err != nil {
				codes <- 0
				return
			}
			req.Header.Set("Authorization", "Bearer flow-test-token")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				codes <- 0
				return
			}
			res.Body.Close()
			codes <- res.StatusCode
		})
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	require.Equal(t, map[int]int{200: 1, 409: 1}, counts)
	got, err := f.server.cfg.Flows.get(t.Context(), f.sid, row.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Revision)
}

func TestManagedFlowsTerminalActionsAndMissingStorage(t *testing.T) {
	f := newManagedFlowFixture(t)
	for _, action := range []string{"finish", "fail", "cancel"} {
		row := createManagedFlow(t, f, action)
		flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/tasks?sessionKey="+f.sid, `{"expectedRevision":1,"status":"blocked","blockedTaskId":"dep"}`, "flow-test-token", 200)
		raw := flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/"+action+"?sessionKey="+f.sid, `{"expectedRevision":2}`, "flow-test-token", 200)
		row = flowRow{}
		require.NoError(t, json.Unmarshal(raw, &row))
		require.Empty(t, row.BlockedTaskID)
		require.EqualValues(t, 3, row.Revision)
		flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/resume?sessionKey="+f.sid, `{"expectedRevision":3}`, "flow-test-token", 409)
	}
	hs := httptest.NewServer(NewServer(Config{Token: "flow-test-token"}).Handler())
	defer hs.Close()
	flowHTTP(t, hs.URL, "GET", "/api/flows?sessionKey="+f.sid, "", "flow-test-token", 503)
}

func TestManagedFlowsRejectMalformedScopeAndPreserveWaitInvariants(t *testing.T) {
	f := newManagedFlowFixture(t)
	f.server.cfg.Agent = &fakeAgent{sessionID: f.sid}
	row := createManagedFlow(t, f, "scope and wait")
	for _, query := range []string{"sessionKey=%zz", "sessionKey=" + f.sid + "&sessionKey=%zz", "sessionKey=" + f.sid + ";bad"} {
		flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/cancel?"+query, `{"expectedRevision":1}`, "flow-test-token", 400)
	}
	badState := `{"nul":"\u0000"}`
	body, _ := json.Marshal(map[string]any{"expectedRevision": 1, "status": "waiting", "stateJson": badState})
	flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/tasks?sessionKey="+f.sid, string(body), "flow-test-token", 400)
	body, _ = json.Marshal(map[string]any{"sessionKey": f.sid, "goal": "invalid PostgreSQL JSONB", "stateJson": badState})
	flowHTTP(t, f.http.URL, "POST", "/api/flows", string(body), "flow-test-token", 400)
	got, err := f.server.cfg.Flows.get(t.Context(), f.sid, row.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, got.Revision)
	require.Equal(t, "running", got.Status)
	flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/tasks?sessionKey="+f.sid, `{"expectedRevision":1,"waitJson":"{\"event\":1}"}`, "flow-test-token", 409)
	flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/tasks?sessionKey="+f.sid, `{"expectedRevision":1,"status":"waiting","waitJson":"{\"event\":1}"}`, "flow-test-token", 200)
	flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/resume?sessionKey="+f.sid, `{"expectedRevision":2,"waitJson":"{\"event\":2}"}`, "flow-test-token", 400)
	got, err = f.server.cfg.Flows.get(t.Context(), f.sid, row.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Revision)
	require.JSONEq(t, `{"event":1}`, got.WaitJSON)
	raw := flowHTTP(t, f.http.URL, "POST", "/api/flows/"+row.ID+"/resume?sessionKey="+f.sid, `{"expectedRevision":2}`, "flow-test-token", 200)
	got = flowRow{}
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "running", got.Status)
	require.JSONEq(t, "{}", got.WaitJSON)
	// Initial wait data is an actual waiting record, never running plus waits.
	raw = flowHTTP(t, f.http.URL, "POST", "/api/flows", `{"sessionKey":"`+f.sid+`","goal":"wait first","waitJson":"{\"approval\":true}"}`, "flow-test-token", 201)
	got = flowRow{}
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "waiting", got.Status)
	require.EqualValues(t, 1, got.Revision)
}

func TestManagedFlowMigrationFailureAndConcurrentRetry(t *testing.T) {
	require.Error(t, NewFlowStore(nil).Migrate(t.Context()))
	database, _ := testpg.Open(t)
	store := NewFlowStore(database)
	require.Error(t, store.Migrate(t.Context()), "sessions must migrate first")
	var missing bool
	require.NoError(t, database.QueryRow(`SELECT to_regclass('web_task_flows') IS NULL`).Scan(&missing))
	require.True(t, missing, "failed migration must roll back")
	_, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { results <- store.Migrate(t.Context()) })
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
}
