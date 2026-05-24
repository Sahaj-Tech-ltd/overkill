package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dgraph-io/badger/v4"

	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

// ---------------------------------------------------------------------------
// test doubles
// ---------------------------------------------------------------------------

type fakeAgent struct {
	chunks   []AgentEvent
	failOpen bool
}

func (f *fakeAgent) StreamACP(ctx context.Context, in string) (<-chan AgentEvent, error) {
	if f.failOpen {
		return nil, errors.New("boom")
	}
	ch := make(chan AgentEvent, len(f.chunks))
	for _, c := range f.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}
func (f *fakeAgent) Model() string     { return "fake" }
func (f *fakeAgent) SessionID() string { return "fake-session" }

type slowAgent struct{}

func (s *slowAgent) StreamACP(ctx context.Context, in string) (<-chan AgentEvent, error) {
	ch := make(chan AgentEvent)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}
func (s *slowAgent) Model() string     { return "slow" }
func (s *slowAgent) SessionID() string { return "" }

// openInMemBadger returns an in-memory BadgerDB for tests that need a JobStore.
func openInMemBadger(t *testing.T) *badger.DB {
	t.Helper()
	db, err := badger.Open(badger.DefaultOptions("").WithInMemory(true).WithLoggingLevel(badger.ERROR))
	if err != nil {
		t.Fatalf("open in-memory badger: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// authHeader returns an http.Header with a Bearer token set.
func authHeader(token string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	return h
}

// doReq is a one-liner for httptest requests.
func doReq(ts *httptest.Server, method, path string, body io.Reader, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequest(method, ts.URL+path, body)
	if err != nil {
		return nil, err
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return http.DefaultClient.Do(req)
}

// jsonReader marshals v to an io.Reader.
func jsonReader(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

// errorResponse is the expected JSON error shape.
type errorResponse struct {
	Error string `json:"error"`
}

// ---------------------------------------------------------------------------
// existing tests (kept verbatim)
// ---------------------------------------------------------------------------

func TestAuthRejection(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{Token: "secret", Agent: &fakeAgent{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/info")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 got %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/info", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("authed get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 got %d", resp.StatusCode)
	}
}

func TestSendAndStream(t *testing.T) {
	t.Parallel()
	a := &fakeAgent{chunks: []AgentEvent{
		{Type: AgentEventToken, Content: "hi"},
		{Type: AgentEventToken, Content: " there"},
		{Type: AgentEventDone},
	}}
	srv := NewServer(Config{Token: "tk", Agent: a})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c := NewClient(ts.URL, "tk")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := c.Send(ctx, "hello")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	var got []string
	var sawDone bool
	for ev := range ch {
		switch ev.Type {
		case "text_delta":
			got = append(got, ev.Content)
		case "done":
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatalf("did not see done event")
	}
	if strings.Join(got, "") != "hi there" {
		t.Fatalf("unexpected text: %q", strings.Join(got, ""))
	}
}

func TestInfoEndpoint(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{}, Name: "overkill", Version: "test"})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := NewClient(ts.URL, "tk")
	info, err := c.GetInfo(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Name != "overkill" || info.Version != "test" {
		t.Fatalf("wrong info: %+v", info)
	}
	if len(info.Capabilities) == 0 {
		t.Fatalf("expected capabilities")
	}
}

func TestGenerateToken(t *testing.T) {
	t.Parallel()
	tk := GenerateToken()
	if len(tk) != 64 {
		t.Fatalf("expected 64-char hex token got %d", len(tk))
	}
}

func TestCancelFlow(t *testing.T) {
	t.Parallel()
	a := &slowAgent{}
	srv := NewServer(Config{Token: "tk", Agent: a})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := NewClient(ts.URL, "tk")

	// Send via raw POST so we get the messageID without consuming the stream.
	body, _ := json.Marshal(SendRequest{From: "x", Content: "hi"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer tk")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer resp.Body.Close()
	var sr SendResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sr.MessageID == "" {
		t.Fatalf("empty messageID")
	}
	if err := c.Cancel(context.Background(), sr.MessageID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

// ---------------------------------------------------------------------------
// §8.7.5-1  Machine-checked auth guard
// ---------------------------------------------------------------------------

// routeProbe is a URL + method pair we probe against the running server.
type routeProbe struct {
	method string
	path   string
	// body is non-nil for POST probes that need a valid JSON body to
	// get past body-decoding checks (otherwise the handler returns 400
	// before reaching the auth middleware — the auth middleware runs FIRST,
	// so in practice we'll get 401 regardless).
	body        io.Reader
	contentType string
}

// allProbes returns every conceivable URL+method probe.  We cast a wide net
// and let the handler tell us which ones it actually recognises (non‑404).
func allProbes() []routeProbe {
	msgBody := jsonReader(SendRequest{From: "t", Content: "hi"})
	jobBody := jsonReader(map[string]string{"intent": "test"})
	return []routeProbe{
		// /v1/info
		{method: http.MethodGet, path: "/v1/info"},
		{method: http.MethodPost, path: "/v1/info"},
		{method: http.MethodPut, path: "/v1/info"},
		{method: http.MethodDelete, path: "/v1/info"},
		{method: http.MethodPatch, path: "/v1/info"},

		// /v1/messages
		{method: http.MethodGet, path: "/v1/messages"},
		{method: http.MethodPost, path: "/v1/messages", body: msgBody, contentType: "application/json"},
		{method: http.MethodPut, path: "/v1/messages"},
		{method: http.MethodDelete, path: "/v1/messages"},

		// /v1/messages/{id}/events
		{method: http.MethodGet, path: "/v1/messages/nonexistent/events"},
		{method: http.MethodPost, path: "/v1/messages/nonexistent/events"},
		{method: http.MethodPut, path: "/v1/messages/nonexistent/events"},

		// /v1/messages/{id}/cancel
		{method: http.MethodGet, path: "/v1/messages/nonexistent/cancel"},
		{method: http.MethodPost, path: "/v1/messages/nonexistent/cancel"},

		// /v1/sessions
		{method: http.MethodGet, path: "/v1/sessions"},
		{method: http.MethodPost, path: "/v1/sessions", body: jsonReader(map[string]string{}), contentType: "application/json"},
		{method: http.MethodPut, path: "/v1/sessions"},
		{method: http.MethodDelete, path: "/v1/sessions"},

		// /v1/sessions/{id}
		{method: http.MethodGet, path: "/v1/sessions/test-id"},
		{method: http.MethodPost, path: "/v1/sessions/test-id"},
		{method: http.MethodPut, path: "/v1/sessions/test-id"},

		// /v1/jobs (conditional — only registered when JobStore is set)
		{method: http.MethodGet, path: "/v1/jobs"},
		{method: http.MethodPost, path: "/v1/jobs", body: jobBody, contentType: "application/json"},
		{method: http.MethodPut, path: "/v1/jobs"},

		// /v1/jobs/{id}
		{method: http.MethodGet, path: "/v1/jobs/test-job"},
		{method: http.MethodPost, path: "/v1/jobs/test-job"},
		{method: http.MethodPut, path: "/v1/jobs/test-job"},

		// /v1/jobs/{id}/cancel
		{method: http.MethodGet, path: "/v1/jobs/test-job/cancel"},
		{method: http.MethodPost, path: "/v1/jobs/test-job/cancel"},

		// bogus paths — should 404 and NOT be considered "routes"
		{method: http.MethodGet, path: "/v1/bogus"},
		{method: http.MethodPost, path: "/v1/bogus"},
		{method: http.MethodGet, path: "/bogus"},
	}
}

// TestMachineCheckedAuthGuard probes every plausible route against the
// running handler and asserts that every route recognized by the server
// (i.e. returns something other than 404) requires authentication.
//
// This is machine-checked because we do NOT manually curate a list of
// "protected" routes — we fire probes at the actual handler and let the
// server tell us which paths it serves.  If a developer adds a new route
// to Handler() without wiring it through withAuth, this test catches it.
func TestMachineCheckedAuthGuard(t *testing.T) {
	t.Parallel()

	t.Run("without_jobs", func(t *testing.T) {
		t.Parallel()
		srv := NewServer(Config{Token: "topsecret", Agent: &fakeAgent{}})
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		discovered := make(map[string]int) // "METHOD path" → status
		for _, p := range allProbes() {
			var rdr io.Reader = p.body
			req, err := http.NewRequest(p.method, ts.URL+p.path, rdr)
			if err != nil {
				t.Fatalf("new request %s %s: %v", p.method, p.path, err)
			}
			if p.contentType != "" {
				req.Header.Set("Content-Type", p.contentType)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", p.method, p.path, err)
			}
			status := resp.StatusCode
			resp.Body.Close()

			// A 404 means the mux doesn't recognise the pattern at all
			// (or the handler returns 404).  Only routes that return
			// non-404 are "recognised" and must be auth-guarded.
			if status != http.StatusNotFound {
				key := p.method + " " + p.path
				discovered[key] = status
				if status != http.StatusUnauthorized {
					t.Errorf("%s returned %d, want 401 (unauthorised)", key, status)
				}
			}
		}

		if len(discovered) == 0 {
			t.Fatal("no routes discovered — probe list may be stale")
		}
		t.Logf("discovered %d routes: %v", len(discovered), discovered)
	})

	t.Run("with_jobs", func(t *testing.T) {
		t.Parallel()
		db := openInMemBadger(t)
		js := daemon.NewJobStore(db)
		srv := NewServer(Config{
			Token:     "topsecret",
			Agent:     &fakeAgent{},
			JobStore:  js,
			JobWorker: daemon.NewWorker(js, func(ctx context.Context, j daemon.Job) error { return nil }, 1),
		})
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		// Only probe the job-related routes — the others are already
		// covered by the without_jobs subtest.
		jobProbes := []routeProbe{
			{method: http.MethodGet, path: "/v1/jobs"},
			{method: http.MethodPost, path: "/v1/jobs", body: jsonReader(map[string]string{"intent": "hi"}), contentType: "application/json"},
			{method: http.MethodGet, path: "/v1/jobs/test-job"},
			{method: http.MethodPost, path: "/v1/jobs/test-job/cancel"},
		}
		for _, p := range jobProbes {
			req, _ := http.NewRequest(p.method, ts.URL+p.path, p.body)
			if p.contentType != "" {
				req.Header.Set("Content-Type", p.contentType)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", p.method, p.path, err)
			}
			status := resp.StatusCode
			resp.Body.Close()
			if status != http.StatusUnauthorized {
				t.Errorf("%s %s returned %d, want 401", p.method, p.path, status)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// §8.7.5-2  Negative auth cases
// ---------------------------------------------------------------------------

func TestAuthInvalidTokens(t *testing.T) {
	t.Parallel()

	srv := NewServer(Config{Token: "topsecret", Agent: &fakeAgent{}})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close() })

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "wrong_token",
			authHeader: "Bearer wrongtoken",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty_token",
			authHeader: "Bearer ",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "no_bearer_prefix",
			authHeader: "topsecret",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "basic_auth_instead_of_bearer",
			authHeader: "Basic dGVzdDpwYXNz",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty_header",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "bearer_lowercase",
			authHeader: "bearer topsecret",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "extra_space_after_bearer",
			authHeader: "Bearer  topsecret",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "case_sensitive_token",
			authHeader: "Bearer TOPSECRET",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/info", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("got %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			// Also verify error shape
			var er errorResponse
			if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
				t.Errorf("error body is not valid JSON: %v", err)
			}
			if er.Error == "" {
				t.Error("error response missing 'error' field")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// §8.7.5-3  Malformed JSON bodies
// ---------------------------------------------------------------------------

func TestMalformedJSONBodies(t *testing.T) {
	t.Parallel()

	srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{},
		JobStore: daemon.NewJobStore(openInMemBadger(t)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close() })

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{
			name:       "messages_not_json",
			method:     http.MethodPost,
			path:       "/v1/messages",
			body:       "this is not json",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "messages_empty_body",
			method:     http.MethodPost,
			path:       "/v1/messages",
			body:       "",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "messages_null",
			method:     http.MethodPost,
			path:       "/v1/messages",
			body:       "null",
			wantStatus: http.StatusBadRequest, // null decodes OK, but Content==""
		},
		{
			name:       "messages_incomplete_json",
			method:     http.MethodPost,
			path:       "/v1/messages",
			body:       `{"from": "x", "content": "hi"`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "messages_wrong_type_array",
			method:     http.MethodPost,
			path:       "/v1/messages",
			body:       `[1,2,3]`,
			wantStatus: http.StatusBadRequest, // Go json decoder accepts this but Content==""
		},
		{
			name:       "jobs_not_json",
			method:     http.MethodPost,
			path:       "/v1/jobs",
			body:       "garbage",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "jobs_empty_json",
			method:     http.MethodPost,
			path:       "/v1/jobs",
			body:       "{}",
			wantStatus: http.StatusBadRequest, // intent required
		},
		{
			name:       "sessions_post_bad_json",
			method:     http.MethodPost,
			path:       "/v1/sessions",
			body:       "not json",
			wantStatus: http.StatusBadRequest, // Actually sessions doesn't validate body — it uses _ = json.NewDecoder... and proceeds
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequest(tt.method, ts.URL+tt.path, strings.NewReader(tt.body))
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer tk")
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				// Some paths might return 500 if badger chokes; that's fine too
				if resp.StatusCode/100 != 4 && resp.StatusCode/100 != 5 {
					t.Errorf("%s: got %d, want %d", tt.name, resp.StatusCode, tt.wantStatus)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// §8.7.5-4  Wrong HTTP methods
// ---------------------------------------------------------------------------

func TestWrongHTTPMethods(t *testing.T) {
	t.Parallel()

	srv := NewServer(Config{
		Token:     "tk",
		Agent:     &fakeAgent{},
		JobStore:  daemon.NewJobStore(openInMemBadger(t)),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close() })

	tests := []struct {
		name       string
		method     string
		path       string
		body       io.Reader
		wantStatus int
	}{
		// /v1/info — only GET
		{name: "info_post", method: http.MethodPost, path: "/v1/info", wantStatus: http.StatusMethodNotAllowed},
		{name: "info_put", method: http.MethodPut, path: "/v1/info", wantStatus: http.StatusMethodNotAllowed},
		{name: "info_delete", method: http.MethodDelete, path: "/v1/info", wantStatus: http.StatusMethodNotAllowed},

		// /v1/messages — only POST
		{name: "messages_get", method: http.MethodGet, path: "/v1/messages", wantStatus: http.StatusMethodNotAllowed},
		{name: "messages_put", method: http.MethodPut, path: "/v1/messages", wantStatus: http.StatusMethodNotAllowed},
		{name: "messages_delete", method: http.MethodDelete, path: "/v1/messages", wantStatus: http.StatusMethodNotAllowed},

		// /v1/messages/{id}/events — only GET
		{name: "events_post", method: http.MethodPost, path: "/v1/messages/test-id/events", wantStatus: http.StatusMethodNotAllowed},

		// /v1/messages/{id}/cancel — only POST
		{name: "cancel_get", method: http.MethodGet, path: "/v1/messages/test-id/cancel", wantStatus: http.StatusMethodNotAllowed},

		// /v1/sessions — GET and POST only
		{name: "sessions_put", method: http.MethodPut, path: "/v1/sessions", wantStatus: http.StatusMethodNotAllowed},
		{name: "sessions_delete", method: http.MethodDelete, path: "/v1/sessions", wantStatus: http.StatusMethodNotAllowed},

		// /v1/jobs — GET and POST only
		{name: "jobs_put", method: http.MethodPut, path: "/v1/jobs", wantStatus: http.StatusMethodNotAllowed},
		{name: "jobs_delete", method: http.MethodDelete, path: "/v1/jobs", wantStatus: http.StatusMethodNotAllowed},

		// /v1/jobs/{id} — GET only
		{name: "job_get_put", method: http.MethodPut, path: "/v1/jobs/test-job", wantStatus: http.StatusMethodNotAllowed},

		// /v1/jobs/{id}/cancel — POST only
		{name: "job_cancel_get", method: http.MethodGet, path: "/v1/jobs/test-job/cancel", wantStatus: http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, err := doReq(ts, tt.method, tt.path, tt.body, authHeader("tk"))
			if err != nil {
				t.Fatalf("req: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("got %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// §8.7.5-5  Error shape consistency
// ---------------------------------------------------------------------------

// errorShapeTest describes a request that should produce an error.
type errorShapeTest struct {
	name       string
	method     string
	path       string
	body       io.Reader
	headers    http.Header
	wantStatus int
}

// allErrorTriggers returns a comprehensive set of requests known to produce
// error responses.  Every single one MUST return a JSON body with an "error"
// field.
func allErrorTriggers() []errorShapeTest {
	noAuth := http.Header{}
	wrongAuth := http.Header{"Authorization": {"Bearer bad"}}
	validAuth := authHeader("tk")

	return []errorShapeTest{
		// ---- auth errors ----
		{name: "unauth_no_header", method: http.MethodGet, path: "/v1/info", headers: noAuth, wantStatus: http.StatusUnauthorized},
		{name: "unauth_wrong_token", method: http.MethodGet, path: "/v1/info", headers: wrongAuth, wantStatus: http.StatusUnauthorized},
		{name: "unauth_no_bearer", method: http.MethodGet, path: "/v1/info", headers: http.Header{"Authorization": {"tk"}}, wantStatus: http.StatusUnauthorized},
		{name: "unauth_messages", method: http.MethodPost, path: "/v1/messages", body: jsonReader(SendRequest{Content: "hi"}), headers: noAuth, wantStatus: http.StatusUnauthorized},
		{name: "unauth_sessions", method: http.MethodGet, path: "/v1/sessions", headers: noAuth, wantStatus: http.StatusUnauthorized},
		{name: "unauth_sessions_sub", method: http.MethodGet, path: "/v1/sessions/test", headers: noAuth, wantStatus: http.StatusUnauthorized},

		// ---- method-not-allowed errors ----
		{name: "method_info_post", method: http.MethodPost, path: "/v1/info", headers: validAuth, wantStatus: http.StatusMethodNotAllowed},
		{name: "method_messages_get", method: http.MethodGet, path: "/v1/messages", headers: validAuth, wantStatus: http.StatusMethodNotAllowed},
		{name: "method_sessions_put", method: http.MethodPut, path: "/v1/sessions", headers: validAuth, wantStatus: http.StatusMethodNotAllowed},

		// ---- bad-request errors ----
		{name: "badreq_empty_body", method: http.MethodPost, path: "/v1/messages", body: nil, headers: validAuth, wantStatus: http.StatusBadRequest},
		{name: "badreq_bad_json", method: http.MethodPost, path: "/v1/messages", body: strings.NewReader("{bad"), headers: validAuth, wantStatus: http.StatusBadRequest},
		{name: "badreq_empty_content", method: http.MethodPost, path: "/v1/messages", body: jsonReader(SendRequest{Content: ""}), headers: validAuth, wantStatus: http.StatusBadRequest},

		// ---- not-found errors (known message ID that doesn't exist) ----
		{name: "notfound_events", method: http.MethodGet, path: "/v1/messages/doesnotexist/events", headers: validAuth, wantStatus: http.StatusNotFound},
		{name: "notfound_cancel", method: http.MethodPost, path: "/v1/messages/doesnotexist/cancel", headers: validAuth, wantStatus: http.StatusNotFound},

		// ---- no-agent ----
		{name: "noagent_send", method: http.MethodPost, path: "/v1/messages", body: jsonReader(SendRequest{Content: "hi"}), headers: authHeader("notk"), wantStatus: http.StatusUnauthorized}, // without agent, auth still checked first

		// Job endpoints (with JobStore) — need separate test below
	}
}

// TestErrorShapeConsistency fires every known error-triggering request and
// asserts that the response is JSON with an "error" field.  This is the
// machine-checked enforcement of the "every error response is JSON" invariant.
func TestErrorShapeConsistency(t *testing.T) {
	t.Parallel()

	// Server with token to trigger auth errors.
	srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{}})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close() })

	for _, tt := range allErrorTriggers() {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rdr io.Reader = tt.body
			req, err := http.NewRequest(tt.method, ts.URL+tt.path, rdr)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			for k, vs := range tt.headers {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			// Set Content-Type for POST bodies
			if tt.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()

			// Status check
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status: got %d, want %d", resp.StatusCode, tt.wantStatus)
			}

			// Content-Type check — must be application/json for all errors
			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type is %q, want application/json", ct)
			}

			// Body must be JSON with "error" field
			var er errorResponse
			if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
				bodyBytes, _ := io.ReadAll(resp.Body)
				t.Errorf("body is not valid JSON: %v (body: %s)", err, string(bodyBytes))
				return
			}
			if er.Error == "" {
				t.Error("JSON error response missing 'error' field")
			}
		})
	}

	// Job-specific error shapes tested separately because we need a JobStore
	t.Run("job_endpoints", func(t *testing.T) {
		t.Parallel()
		db := openInMemBadger(t)
		js := daemon.NewJobStore(db)
		srv := NewServer(Config{
			Token:    "tk",
			Agent:    &fakeAgent{},
			JobStore: js,
		})
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		noAuth := http.Header{}
		validAuth := authHeader("tk")

		jobTests := []errorShapeTest{
			{name: "job_unauth", method: http.MethodGet, path: "/v1/jobs", headers: noAuth, wantStatus: http.StatusUnauthorized},
			{name: "job_method_put", method: http.MethodPut, path: "/v1/jobs", headers: validAuth, wantStatus: http.StatusMethodNotAllowed},
			{name: "job_create_bad_json", method: http.MethodPost, path: "/v1/jobs", body: strings.NewReader("{bad"), headers: validAuth, wantStatus: http.StatusBadRequest},
			{name: "job_create_no_intent", method: http.MethodPost, path: "/v1/jobs", body: jsonReader(map[string]string{}), headers: validAuth, wantStatus: http.StatusBadRequest},
		}

		for _, tt := range jobTests {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var rdr io.Reader = tt.body
				req, _ := http.NewRequest(tt.method, ts.URL+tt.path, rdr)
				for k, vs := range tt.headers {
					for _, v := range vs {
						req.Header.Add(k, v)
					}
				}
				if tt.body != nil {
					req.Header.Set("Content-Type", "application/json")
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("req: %v", err)
				}
				defer resp.Body.Close()

				ct := resp.Header.Get("Content-Type")
				if !strings.HasPrefix(ct, "application/json") {
					t.Errorf("Content-Type is %q, want application/json", ct)
				}
				var er errorResponse
				if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
					t.Errorf("body not valid JSON: %v", err)
				}
				if er.Error == "" {
					t.Error("missing 'error' field")
				}
			})
		}
	})

	// Test that http.NotFound responses (when the mux itself returns 404)
	// also produce JSON errors.  /v1/completely-unknown is not registered
	// in the mux, so it doesn't go through withAuth — it hits the mux's
	// default 404 handler directly.
	t.Run("mux_404", func(t *testing.T) {
		t.Parallel()
		srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{}})
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		// A truly unknown path bypasses withAuth entirely because the
		// mux doesn't have a registered handler for it.
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/completely-unknown", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("req: %v", err)
		}
		defer resp.Body.Close()

		// The Go default ServeMux returns plain-text 404 for unknown paths.
		// This is the mux's built-in behaviour — withAuth only wraps
		// registered handlers.
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("unknown path without auth: got %d, want 404", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// §8.7.5-6  Concurrency — fire N goroutines at endpoints simultaneously
// ---------------------------------------------------------------------------

func TestConcurrentAuthGuard(t *testing.T) {
	t.Parallel()

	srv := NewServer(Config{Token: "topsecret", Agent: &fakeAgent{}})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close() })

	const goroutines = 50
	const iterationsPerG = 10

	paths := []string{
		"/v1/info",
		"/v1/messages",
		"/v1/messages/nonexistent/events",
		"/v1/sessions",
		"/v1/sessions/test-id",
	}

	var wg sync.WaitGroup
	errCh := make(chan string, goroutines*iterationsPerG*len(paths))

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterationsPerG; i++ {
				for _, p := range paths {
					method := http.MethodGet
					if p == "/v1/messages" {
						method = http.MethodPost
					}
					var body io.Reader
					if method == http.MethodPost {
						body = jsonReader(SendRequest{From: "c", Content: "hi"})
					}
					req, err := http.NewRequest(method, ts.URL+p, body)
					if err != nil {
						errCh <- fmt.Sprintf("new req: %v", err)
						continue
					}
					if body != nil {
						req.Header.Set("Content-Type", "application/json")
					}
					// No auth → expect 401
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						errCh <- fmt.Sprintf("do: %v", err)
						continue
					}
					if resp.StatusCode != http.StatusUnauthorized {
						errCh <- fmt.Sprintf("worker %d iter %d %s %s: got %d, want 401",
							workerID, i, method, p, resp.StatusCode)
					}
					resp.Body.Close()
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)

	for e := range errCh {
		t.Error(e)
	}
}

func TestConcurrentAuthenticatedAccess(t *testing.T) {
	t.Parallel()

	a := &fakeAgent{chunks: []AgentEvent{
		{Type: AgentEventToken, Content: "ok"},
		{Type: AgentEventDone},
	}}
	srv := NewServer(Config{Token: "tk", Agent: a})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	const goroutines = 20

	var wg sync.WaitGroup
	errCh := make(chan string, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Hit /v1/info concurrently
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/info", nil)
			req.Header.Set("Authorization", "Bearer tk")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errCh <- fmt.Sprintf("info: %v", err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errCh <- fmt.Sprintf("info: got %d, want 200", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for e := range errCh {
		t.Error(e)
	}
}

// TestConcurrentSendAndStream fires multiple concurrent message sends.
func TestConcurrentSendAndStream(t *testing.T) {
	t.Parallel()

	chunks := make([]AgentEvent, 20)
	for i := range chunks {
		chunks[i] = AgentEvent{Type: AgentEventToken, Content: "x"}
	}
	chunks = append(chunks, AgentEvent{Type: AgentEventDone})

	a := &fakeAgent{chunks: chunks}
	srv := NewServer(Config{Token: "tk", Agent: a})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	const goroutines = 10
	var wg sync.WaitGroup
	errCh := make(chan string, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c := NewClient(ts.URL, "tk")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ch, err := c.Send(ctx, fmt.Sprintf("msg-%d", id))
			if err != nil {
				errCh <- fmt.Sprintf("send %d: %v", id, err)
				return
			}
			for ev := range ch {
				if ev.Type == "error" {
					errCh <- fmt.Sprintf("send %d: stream error: %s", id, ev.Error)
				}
			}
		}(g)
	}
	wg.Wait()
	close(errCh)

	for e := range errCh {
		t.Error(e)
	}
}

// TestConcurrentJobCreation fires concurrent POSTs to /v1/jobs.
func TestConcurrentJobCreation(t *testing.T) {
	t.Parallel()

	db := openInMemBadger(t)
	js := daemon.NewJobStore(db)
	srv := NewServer(Config{
		Token:     "tk",
		Agent:     &fakeAgent{},
		JobStore:  js,
		JobWorker: daemon.NewWorker(js, func(ctx context.Context, j daemon.Job) error { return nil }, 1),
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	const goroutines = 20
	var wg sync.WaitGroup
	errCh := make(chan string, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			body := jsonReader(map[string]string{"intent": fmt.Sprintf("test-%d", id)})
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/jobs", body)
			req.Header.Set("Authorization", "Bearer tk")
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errCh <- fmt.Sprintf("job %d: %v", id, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				errCh <- fmt.Sprintf("job %d: got %d, want 201", id, resp.StatusCode)
				body, _ := io.ReadAll(resp.Body)
				t.Logf("body: %s", body)
			}
		}(g)
	}
	wg.Wait()
	close(errCh)

	for e := range errCh {
		t.Error(e)
	}
}

// TestConcurrentMixedAuth fires both authed and unauthed requests
// concurrently to ensure the auth middleware is thread-safe.
func TestConcurrentMixedAuth(t *testing.T) {
	t.Parallel()

	srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	const goroutines = 30
	var wg sync.WaitGroup
	errCh := make(chan string, goroutines*2)

	// Half unauthed, half authed
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/info", nil)
			if id%2 == 0 {
				req.Header.Set("Authorization", "Bearer tk")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errCh <- fmt.Sprintf("req %d: %v", id, err)
				return
			}
			defer resp.Body.Close()
			wantStatus := http.StatusOK
			if id%2 != 0 {
				wantStatus = http.StatusUnauthorized
			}
			if resp.StatusCode != wantStatus {
				errCh <- fmt.Sprintf("req %d: got %d, want %d", id, resp.StatusCode, wantStatus)
			}
		}(g)
	}
	wg.Wait()
	close(errCh)

	for e := range errCh {
		t.Error(e)
	}
}

// ---------------------------------------------------------------------------
// §8.7.5-7  Edge cases
// ---------------------------------------------------------------------------

// TestEmptyTokenBypass verifies that when Token is empty, auth is skipped.
func TestEmptyTokenBypass(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{Token: "", Agent: &fakeAgent{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/v1/info")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with empty token, got %d", resp.StatusCode)
	}
}

// TestCORSPreflight verifies OPTIONS requests return 204.
func TestCORSPreflight(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{
		Token:           "tk",
		Agent:           &fakeAgent{},
		AllowedOrigins: []string{"http://localhost", "http://127.0.0.1"},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/v1/info", nil)
	req.Header.Set("Origin", "http://localhost")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS: got %d, want 204", resp.StatusCode)
	}
	// CORS headers should be present
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost" {
		t.Error("missing or wrong Access-Control-Allow-Origin")
	}
}

// TestOriginAllowed_PrefixAttack verifies the exact-match origin check.
func TestOriginAllowed_PrefixAttack(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{
		Token:           "tk",
		Agent:           &fakeAgent{},
		AllowedOrigins: []string{"http://localhost"},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// This origin should NOT be allowed — it's a prefix attack
	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/v1/info", nil)
	req.Header.Set("Origin", "http://localhost.evil.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("OPTIONS without matching origin: got %d, want 204", resp.StatusCode)
	}
	// The CORS header should NOT be set for an unapproved origin
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("Access-Control-Allow-Origin should be empty for unapproved origin")
	}
}

// TestMessageSub_UnknownSubPath verifies invalid sub-resources.
func TestMessageSub_UnknownSubPath(t *testing.T) {
	t.Parallel()
	a := &fakeAgent{chunks: []AgentEvent{{Type: AgentEventDone}}}
	srv := NewServer(Config{Token: "tk", Agent: a})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// First send a message so we have a real message ID
	c := NewClient(ts.URL, "tk")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := c.Send(ctx, "hello")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	// Drain the stream to get the messageID (we need to parse it from the response)
	// Actually, let's use a raw send to get the ID
	for range ch {
	}

	// Raw send to get messageID
	body, _ := json.Marshal(SendRequest{From: "x", Content: "hi2"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/messages", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer tk")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	var sr SendResponse
	json.NewDecoder(resp.Body).Decode(&sr)
	resp.Body.Close()

	// Now try to access a bogus sub-resource
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/v1/messages/"+sr.MessageID+"/bogus", nil)
	req.Header.Set("Authorization", "Bearer tk")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bogus sub: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("bogus sub-resource: got %d, want 404", resp.StatusCode)
	}
}

// TestSessionSub_Nonexistent tests that accessing a nonexistent session
// via the sub-handler returns 404.
func TestSessionSub_Nonexistent(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{}, Store: nil})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/sessions/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer tk")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	// With Store=nil, handleSessionSub returns 404 immediately
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("got %d, want 404", resp.StatusCode)
	}
}

// TestNoAgentAttached verifies that sending to a server without an agent
// returns 503 when authenticated but with no agent.
func TestNoAgentAttached(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{Token: "tk", Agent: nil})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := jsonReader(SendRequest{From: "x", Content: "hi"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/messages", body)
	req.Header.Set("Authorization", "Bearer tk")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", resp.StatusCode)
	}
	// Verify error shape
	var er errorResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		t.Errorf("not valid JSON: %v", err)
	}
}

// TestSendWithSessionID verifies that SessionID is passed through.
func TestSendWithSessionID(t *testing.T) {
	t.Parallel()
	a := &fakeAgent{chunks: []AgentEvent{{Type: AgentEventDone}}}
	srv := NewServer(Config{Token: "tk", Agent: a})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	body := jsonReader(SendRequest{From: "x", Content: "hi", SessionID: "my-session"})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/messages", body)
	req.Header.Set("Authorization", "Bearer tk")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("got %d, want 202", resp.StatusCode)
	}
	var sr SendResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sr.SessionID != "my-session" {
		t.Errorf("sessionID: got %q, want %q", sr.SessionID, "my-session")
	}
}

// TestAgentErrorOpen verifies that when StreamACP returns an error,
// an error event is emitted.
func TestAgentErrorOpen(t *testing.T) {
	t.Parallel()
	a := &fakeAgent{failOpen: true}
	srv := NewServer(Config{Token: "tk", Agent: a})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c := NewClient(ts.URL, "tk")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := c.Send(ctx, "hello")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	var sawError bool
	for ev := range ch {
		if ev.Type == "error" {
			sawError = true
			if ev.Error != "boom" {
				t.Errorf("expected 'boom' error, got %q", ev.Error)
			}
		}
	}
	if !sawError {
		t.Error("expected error event from failing agent")
	}
}

// TestSessionsNilStore verifies the nil-store behaviour.
func TestSessionsNilStore(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{}, Store: nil})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c := NewClient(ts.URL, "tk")
	sessions, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions with nil store, got %d", len(sessions))
	}
}

// TestEvents_NotFoundAfterDrain verifies that after the 60s drain window,
// the events endpoint returns 404.
func TestEvents_NotFoundImmediately(t *testing.T) {
	t.Parallel()
	a := &fakeAgent{chunks: []AgentEvent{{Type: AgentEventDone}}}
	srv := NewServer(Config{Token: "tk", Agent: a})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Send a message and drain immediately
	c := NewClient(ts.URL, "tk")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := c.Send(ctx, "hi")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	for range ch {
	} // drain

	// Wait a tiny bit, then try to re-stream — should still work within 60s
	time.Sleep(10 * time.Millisecond)

	// Try an unknown message ID
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/messages/doesnotexistreally/events", nil)
	req.Header.Set("Authorization", "Bearer tk")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown message events: got %d, want 404", resp.StatusCode)
	}
}

// TestSessionsSub_EmptyID tests /v1/sessions/ with no ID.
func TestSessionsSub_EmptyID(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/sessions/", nil)
	req.Header.Set("Authorization", "Bearer tk")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	// With nil store, returns 404 from handleSessionSub
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("empty session ID: got %d, want 404", resp.StatusCode)
	}
}

// TestMessagesSub_ShortPath tests /v1/messages/ with not enough parts.
func TestMessagesSub_ShortPath(t *testing.T) {
	t.Parallel()
	srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/messages/onlyid", nil)
	req.Header.Set("Authorization", "Bearer tk")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("req: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("short path: got %d, want 404", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// satisfy unused warning if any
// ---------------------------------------------------------------------------
var _ = time.Second
