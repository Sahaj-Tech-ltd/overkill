package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// fakeAgent emits a canned event sequence; lets us test the server without
// the real LLM stack.
type fakeAgent struct {
	model     string
	sessionID string
	events    []agent.StreamEvent
	// streamErr, if set, is returned by Stream() instead of the event channel.
	streamErr error
}

func (f *fakeAgent) Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error) {
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	out := make(chan agent.StreamEvent, len(f.events))
	go func() {
		defer close(out)
		for _, ev := range f.events {
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
	}()
	return out, nil
}
func (f *fakeAgent) Model() string          { return f.model }
func (f *fakeAgent) SessionID() string      { return f.sessionID }
func (f *fakeAgent) SetSessionID(id string) { f.sessionID = id }

func newTestServer(t *testing.T, token string) (*Server, *httptest.Server) {
	t.Helper()
	srv := NewServer(Config{Token: token, Provider: "openai", Version: "test", Agent: &fakeAgent{
		model: "gpt-test",
		events: []agent.StreamEvent{
			{Type: agent.EventToken, Content: "hi "},
			{Type: agent.EventToken, Content: "there"},
			{Type: agent.EventDone},
		},
	}})
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return srv, hs
}

// fakeStore is an in-memory session store for tests.
type fakeStore struct {
	mu    sync.Mutex
	sess  map[string]*session.Session
	order []string // insertion order
}

func newFakeStore() *fakeStore {
	return &fakeStore{sess: make(map[string]*session.Session)}
}

func (fs *fakeStore) Create(_ context.Context, s *session.Session) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.sess[s.ID] = s
	fs.order = append(fs.order, s.ID)
	return nil
}

func (fs *fakeStore) Load(_ context.Context, id string) (*session.Session, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	s, ok := fs.sess[id]
	if !ok {
		return nil, fmt.Errorf("session %s not found", id)
	}
	return s, nil
}

func (fs *fakeStore) Save(_ context.Context, s *session.Session) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.sess[s.ID] = s
	return nil
}

func (fs *fakeStore) Delete(_ context.Context, id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	delete(fs.sess, id)
	return nil
}

func (fs *fakeStore) List(_ context.Context, opts session.ListOptions) ([]*session.Session, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := make([]*session.Session, 0, len(fs.order))
	for _, id := range fs.order {
		out = append(out, fs.sess[id])
	}
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

func (fs *fakeStore) Close() error { return nil }

// ----- auth ----------------------------------------------------------------

func TestAuthRejection(t *testing.T) {
	_, hs := newTestServer(t, "secret")
	tests := []struct {
		name   string
		header string
		query  string
		cookie string
		want   int
	}{
		{"no creds", "", "", "", http.StatusUnauthorized},
		{"bad bearer", "Bearer wrong", "", "", http.StatusUnauthorized},
		{"good bearer", "Bearer secret", "", "", http.StatusOK},
		// Query-param tokens are no longer accepted — they leak through
		// access logs and browser history. Bearer header / cookie only.
		{"query ignored", "", "?t=secret", "", http.StatusUnauthorized},
		{"good cookie", "", "", "secret", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", hs.URL+"/api/info"+tc.query, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "overkill-token", Value: tc.cookie})
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				t.Errorf("got %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

// ----- /api/info -----------------------------------------------------------

func TestInfoEndpoint(t *testing.T) {
	_, hs := newTestServer(t, "")
	res, err := http.Get(hs.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got infoResponse
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Provider != "openai" || got.Model != "gpt-test" || got.Version != "test" {
		t.Errorf("unexpected info: %+v", got)
	}
}

func TestInfoWithoutAgent(t *testing.T) {
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test"})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	res, err := http.Get(hs.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got infoResponse
	json.NewDecoder(res.Body).Decode(&got)
	if got.Model != "" || got.SessionID != "" {
		t.Errorf("expected empty model/session when no agent; got model=%q session=%q", got.Model, got.SessionID)
	}
}

// ----- /api/models ---------------------------------------------------------

func TestModelsEmpty(t *testing.T) {
	_, hs := newTestServer(t, "")
	res, err := http.Get(hs.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "[]") {
		t.Errorf("expected empty list, got %q", body)
	}
}

func TestModelsWithCatalog(t *testing.T) {
	catalogJSON := `{
		"openai": {
			"id": "openai",
			"name": "OpenAI",
			"models": {
				"gpt-4o": {"id": "gpt-4o", "name": "GPT-4o"},
				"gpt-4o-mini": {"id": "gpt-4o-mini", "name": "GPT-4o Mini"}
			}
		},
		"anthropic": {
			"id": "anthropic",
			"name": "Anthropic",
			"models": {
				"claude-sonnet-4": {"id": "claude-sonnet-4", "name": "Claude Sonnet 4"}
			}
		}
	}`
	cat, err := providers.ParseCatalog([]byte(catalogJSON), providers.SourceLive)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Catalog: cat})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	res, err := http.Get(hs.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	var models []modelRow
	if err := json.NewDecoder(res.Body).Decode(&models); err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d: %+v", len(models), models)
	}
	// Check we have both providers
	seen := make(map[string]bool)
	for _, m := range models {
		if m.Provider == "" {
			t.Errorf("model %s has empty provider", m.ID)
		}
		seen[m.Provider] = true
	}
	if !seen["openai"] || !seen["anthropic"] {
		t.Errorf("missing providers; got %v", seen)
	}
}

// ----- /api/sessions --------------------------------------------------------

func TestSessionsEmpty(t *testing.T) {
	_, hs := newTestServer(t, "")
	res, err := http.Get(hs.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestSessionsListWithStore(t *testing.T) {
	store := newFakeStore()
	_ = store.Create(context.Background(), &session.Session{
		ID: "s1", Title: "First", UpdatedAt: time.Now(),
	})
	_ = store.Create(context.Background(), &session.Session{
		ID: "s2", Title: "Second", UpdatedAt: time.Now(),
	})

	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Store: store})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	res, err := http.Get(hs.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	var rows []sessionRow
	if err := json.NewDecoder(res.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(rows))
	}
}

func TestSessionsCreate(t *testing.T) {
	store := newFakeStore()
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Store: store})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	body := strings.NewReader(`{"title":"new session","folder":"work"}`)
	res, err := http.Post(hs.URL+"/api/sessions", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", res.StatusCode)
	}

	var created session.Session
	json.NewDecoder(res.Body).Decode(&created)
	if created.Title != "new session" {
		t.Errorf("title = %q, want 'new session'", created.Title)
	}
	if created.Folder != "work" {
		t.Errorf("folder = %q, want 'work'", created.Folder)
	}

	// Verify it's persisted
	loaded, err := store.Load(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("session not persisted: %v", err)
	}
	if loaded.Title != "new session" {
		t.Errorf("persisted title = %q", loaded.Title)
	}
}

func TestSessionsMethodNotAllowed(t *testing.T) {
	// Need Store set so the handler hits the method switch (without Store,
	// it returns 200 early for all methods — existing behavior).
	store := newFakeStore()
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Store: store})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	req, _ := http.NewRequest(http.MethodPut, hs.URL+"/api/sessions", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", res.StatusCode)
	}
}

// ----- /api/sessions/{id} --------------------------------------------------

func TestSessionSubGet(t *testing.T) {
	store := newFakeStore()
	sess := &session.Session{ID: "abc", Title: "Test Session", UpdatedAt: time.Now()}
	store.Create(context.Background(), sess)

	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Store: store})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	res, err := http.Get(hs.URL + "/api/sessions/abc")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}
	var got session.Session
	json.NewDecoder(res.Body).Decode(&got)
	if got.ID != "abc" || got.Title != "Test Session" {
		t.Errorf("got ID=%q Title=%q Folder=%q CreatedAt=%v UpdatedAt=%v", got.ID, got.Title, got.Folder, got.CreatedAt, got.UpdatedAt)
	}
}

func TestSessionSubNotFound(t *testing.T) {
	store := newFakeStore()
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Store: store})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	res, err := http.Get(hs.URL + "/api/sessions/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", res.StatusCode)
	}
}

func TestSessionSubDelete(t *testing.T) {
	store := newFakeStore()
	store.Create(context.Background(), &session.Session{ID: "delme", Title: "Delete Me"})

	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Store: store})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	req, _ := http.NewRequest(http.MethodDelete, hs.URL+"/api/sessions/delme", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res.StatusCode)
	}

	// Verify deleted
	if _, err := store.Load(context.Background(), "delme"); err == nil {
		t.Error("session should be deleted")
	}
}

func TestSessionSubRename(t *testing.T) {
	store := newFakeStore()
	store.Create(context.Background(), &session.Session{ID: "rename-me", Title: "Old Title"})

	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Store: store})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	body := strings.NewReader(`{"title":"New Title"}`)
	res, err := http.Post(hs.URL+"/api/sessions/rename-me/rename", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var updated session.Session
	json.NewDecoder(res.Body).Decode(&updated)
	if updated.Title != "New Title" {
		t.Errorf("title = %q, want 'New Title'", updated.Title)
	}
}

func TestSessionSubMethodNotAllowed(t *testing.T) {
	store := newFakeStore()
	store.Create(context.Background(), &session.Session{ID: "x", Title: "X"})
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Store: store})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	req, _ := http.NewRequest(http.MethodPut, hs.URL+"/api/sessions/x", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", res.StatusCode)
	}
}

func TestSessionSubWithoutStore(t *testing.T) {
	_, hs := newTestServer(t, "")
	res, err := http.Get(hs.URL + "/api/sessions/any")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 without store, got %d", res.StatusCode)
	}
}

// ----- /api/send -----------------------------------------------------------

func TestSendAndCancel(t *testing.T) {
	srv, hs := newTestServer(t, "")
	body := strings.NewReader(`{"sessionId":"s1","text":"hello"}`)
	res, err := http.Post(hs.URL+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("send status %d", res.StatusCode)
	}
	var sr sendResponse
	_ = json.NewDecoder(res.Body).Decode(&sr)
	if sr.MessageID == "" {
		t.Errorf("missing messageId")
	}

	// give the goroutine time to register itself, then cancel.
	time.Sleep(20 * time.Millisecond)
	res2, err := http.Post(hs.URL+"/api/cancel", "application/json", strings.NewReader(`{"sessionId":"s1"}`))
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	_ = srv
}

func TestSendNoAgent(t *testing.T) {
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test"})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	body := strings.NewReader(`{"text":"hello"}`)
	res, err := http.Post(hs.URL+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 without agent, got %d", res.StatusCode)
	}
}

func TestSendBadJSON(t *testing.T) {
	_, hs := newTestServer(t, "")
	body := strings.NewReader(`not json`)
	res, err := http.Post(hs.URL+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", res.StatusCode)
	}
}

func TestSendEmptyText(t *testing.T) {
	_, hs := newTestServer(t, "")
	body := strings.NewReader(`{"text":"","sessionId":"s1"}`)
	res, err := http.Post(hs.URL+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for empty text, got %d", res.StatusCode)
	}
}

func TestSendPOSTOnly(t *testing.T) {
	_, hs := newTestServer(t, "")
	res, err := http.Get(hs.URL + "/api/send")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET /api/send, got %d", res.StatusCode)
	}
}

func TestSendWithDefaultSessionID(t *testing.T) {
	ag := &fakeAgent{model: "gpt", sessionID: "default-sid",
		events: []agent.StreamEvent{{Type: agent.EventDone}}}
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Agent: ag})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	// Send without sessionId — should use agent's default
	body := strings.NewReader(`{"text":"hi"}`)
	res, err := http.Post(hs.URL+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.StatusCode)
	}
	var sr sendResponse
	json.NewDecoder(res.Body).Decode(&sr)
	if sr.SessionID != "default-sid" {
		t.Errorf("expected sessionId default-sid, got %q", sr.SessionID)
	}
}

// ----- /api/cancel ---------------------------------------------------------

func TestCancelNonexistent(t *testing.T) {
	_, hs := newTestServer(t, "")
	body := strings.NewReader(`{"sessionId":"does-not-exist"}`)
	res, err := http.Post(hs.URL+"/api/cancel", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	// Should succeed even if nothing to cancel
	if res.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", res.StatusCode)
	}
}

// ----- isLoopbackAddr ------------------------------------------------------

func TestIsLoopbackAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8420", true},
		{"127.0.0.2:8420", true},
		{"::1:8420", false}, // must be bracketed; SplitHostPort can't parse bare IPv6:port
		{"[::1]:8420", true},
		{"localhost:8420", true},
		{"LOCALHOST:8420", true}, // case-insensitive
		{"192.168.1.1:8420", false},
		{"0.0.0.0:8420", false},
		{"10.0.0.1:8420", false},
		{":8420", false},                   // all interfaces
		{"127.0.0.1.evil.com:8420", false}, // prefix-match trap
		{"!27.0.0.1:8420", false},          // not an IP
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			got := isLoopbackAddr(tc.addr)
			if got != tc.want {
				t.Errorf("isLoopbackAddr(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// ----- limitBody -----------------------------------------------------------

func TestLimitBodyRejectsLargeRequest(t *testing.T) {
	_, hs := newTestServer(t, "")
	// Send more than maxRequestBody (1MB). MaxBytesReader causes the
	// body Read to fail when the limit is exceeded, which json.Decode
	// sees as a bad request (400), not 413. The body IS capped — the
	// handler just surfaces it as a decode error.
	big := strings.NewReader(`{"text":"` + strings.Repeat("x", maxRequestBody+100) + `"}`)
	res, err := http.Post(hs.URL+"/api/send", "application/json", big)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for oversized body (decode failure), got %d", res.StatusCode)
	}
}

// ----- cacheStatic ---------------------------------------------------------

func TestCacheStaticHeader(t *testing.T) {
	_, hs := newTestServer(t, "")
	res, err := http.Get(hs.URL + "/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	cc := res.Header.Get("Cache-Control")
	if cc != "public, max-age=86400" {
		t.Errorf("Cache-Control = %q, want 'public, max-age=86400'", cc)
	}
}

// ----- Static / index -------------------------------------------------------

func TestStaticIndex(t *testing.T) {
	_, hs := newTestServer(t, "")
	res, err := http.Get(hs.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "<title>overkill</title>") {
		t.Errorf("index missing title; got %d bytes", len(body))
	}
}

// ----- eventBus -------------------------------------------------------------

func TestEventBusSubscribePublish(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe()

	bus.publish("s1", wsEvent{Type: "test", Content: "hello"})

	select {
	case ev := <-sub.ch:
		if ev.Type != "test" || ev.Content != "hello" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBusUnsubscribe(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe()
	bus.unsubscribe(sub)

	bus.publish("s1", wsEvent{Type: "test"})

	select {
	case ev, ok := <-sub.ch:
		if ok {
			t.Errorf("expected closed channel after unsubscribe, got %+v", ev)
		}
	case <-time.After(50 * time.Millisecond):
		// channel should be closed
	}
}

func TestEventBusSessionIDFallback(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe()

	// Publish with no SessionID on the event — should get backfilled
	bus.publish("sid-123", wsEvent{Type: "test"})

	select {
	case ev := <-sub.ch:
		if ev.SessionID != "sid-123" {
			t.Errorf("expected SessionID sid-123, got %q", ev.SessionID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out")
	}
}

func TestEventBusMultipleSubscribers(t *testing.T) {
	bus := newEventBus()
	s1 := bus.subscribe()
	s2 := bus.subscribe()

	bus.publish("s1", wsEvent{Type: "ping"})

	for i, sub := range []*subscription{s1, s2} {
		select {
		case ev := <-sub.ch:
			if ev.Type != "ping" {
				t.Errorf("sub %d: unexpected type %q", i, ev.Type)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("sub %d: timed out", i)
		}
	}
}

func TestEventBusSlowSubscriberDropped(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe()

	// Fill the channel buffer (64 capacity) so the next publish drops
	for i := 0; i < 64; i++ {
		bus.publish("s", wsEvent{Type: "fill", Content: fmt.Sprintf("%d", i)})
	}

	// Now publish one more — the channel is full, so this should be dropped
	bus.publish("s", wsEvent{Type: "dropped"})

	// Drain and verify no "dropped" event
	close(sub.ch)
	found := false
	for ev := range sub.ch {
		if ev.Type == "dropped" {
			found = true
		}
	}
	if found {
		t.Error("dropped event was received despite full channel")
	}
}

func TestEventBusConcurrentPublish(t *testing.T) {
	bus := newEventBus()
	sub := bus.subscribe()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			bus.publish("s1", wsEvent{Type: "concurrent", Content: fmt.Sprintf("%d", n)})
		}(i)
	}

	// Drain in a goroutine with its own counter
	var count int
	var drainWg sync.WaitGroup
	drainWg.Add(1)
	go func() {
		defer drainWg.Done()
		for range sub.ch {
			count++
		}
	}()

	wg.Wait()
	bus.unsubscribe(sub)
	drainWg.Wait()

	if count < 50 {
		t.Logf("only received %d of 50 events (some may have been dropped)", count)
	}
}

// ----- Shutdown -------------------------------------------------------------

func TestShutdownCancelsStreams(t *testing.T) {
	ag := &fakeAgent{
		model: "gpt",
		events: []agent.StreamEvent{
			{Type: agent.EventToken, Content: "streaming..."},
			// No EventDone, so the stream runs indefinitely
		},
	}
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test", Agent: ag})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()

	// Start a stream
	body := strings.NewReader(`{"sessionId":"shutdown-test","text":"hi"}`)
	res, err := http.Post(hs.URL+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	// Shutdown should cancel it
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("shutdown error: %v", err)
	}

	// Verify stream cleanup
	srv.mu.Lock()
	if _, exists := srv.streams["shutdown-test"]; exists {
		t.Error("stream should be cleaned up after shutdown")
	}
	srv.mu.Unlock()
}

func TestShutdownNilHTTPSrv(t *testing.T) {
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test"})
	// Shutdown without Start
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("shutdown on unstarted server should not error, got %v", err)
	}
}
