package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
)

// fakeAgent emits a canned event sequence; lets us test the server without
// the real LLM stack.
type fakeAgent struct {
	model     string
	sessionID string
	events    []agent.StreamEvent
}

func (f *fakeAgent) Stream(ctx context.Context, in string) (<-chan agent.StreamEvent, error) {
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
func (f *fakeAgent) Model() string         { return f.model }
func (f *fakeAgent) SessionID() string     { return f.sessionID }
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
		{"good query", "", "?t=secret", "", http.StatusOK},
		{"good cookie", "", "", "secret", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", hs.URL+"/api/info"+tc.query, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "ethos-token", Value: tc.cookie})
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

func TestStaticIndex(t *testing.T) {
	_, hs := newTestServer(t, "")
	res, err := http.Get(hs.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "<title>ethos</title>") {
		t.Errorf("index missing title; got %d bytes", len(body))
	}
}
