package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

type fakeAgent struct{ sid string }

func (f *fakeAgent) SessionID() string     { return f.sid }
func (f *fakeAgent) SetSessionID(id string) { f.sid = id }
func (f *fakeAgent) Stream(_ context.Context, _ string) (<-chan agent.StreamEvent, error) {
	ch := make(chan agent.StreamEvent, 2)
	ch <- agent.StreamEvent{Type: agent.EventToken, Content: "pong"}
	close(ch)
	return ch, nil
}

func TestBridge_InboundFansOutToSSE(t *testing.T) {
	r, _ := gateway.NewSessionRouter("")
	d := gateway.NewDispatcher(&fakeAgent{}, r)
	d.UpdateEvery = 5 * time.Millisecond
	b := New(d, "", "")

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/in", b.handleIn)
	mux.HandleFunc("/v1/out", b.handleOut)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Subscribe first.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/out?channel=whatsapp", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Post inbound.
	body, _ := json.Marshal(InboundPayload{
		Channel: "whatsapp", ChatKey: "+15551234", Text: "ping",
	})
	postResp, err := http.Post(srv.URL+"/v1/in", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()

	// Read SSE frames until we see a "final" or time out.
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 4096)
		var collected strings.Builder
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				collected.Write(buf[:n])
				if strings.Contains(collected.String(), `"kind":"final"`) {
					got <- collected.String()
					return
				}
			}
			if err != nil {
				got <- collected.String()
				return
			}
		}
	}()
	select {
	case s := <-got:
		if !strings.Contains(s, `"kind":"post"`) || !strings.Contains(s, "pong") {
			t.Fatalf("missing expected frames: %q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE frames")
	}
}

func TestBridge_AuthRejectsBadToken(t *testing.T) {
	b := New(gateway.NewDispatcher(&fakeAgent{}, nil), "secret", "")
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/in", b.handleIn)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body, _ := json.Marshal(InboundPayload{Channel: "x", ChatKey: "1", Text: "y"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/in", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d want 401", resp.StatusCode)
	}
}
