package acp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
	srv := NewServer(Config{Token: "tk", Agent: &fakeAgent{}, Name: "ethos", Version: "test"})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := NewClient(ts.URL, "tk")
	info, err := c.GetInfo(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Name != "ethos" || info.Version != "test" {
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

// satisfy unused warning if any
var _ = time.Second
