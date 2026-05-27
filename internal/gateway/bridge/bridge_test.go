package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

type fakeAgent struct{ sid string }

func (f *fakeAgent) SessionID() string      { return f.sid }
func (f *fakeAgent) SetSessionID(id string) { f.sid = id }
func (f *fakeAgent) Stream(_ context.Context, _ string) (<-chan agent.StreamEvent, error) {
	ch := make(chan agent.StreamEvent, 2)
	ch <- agent.StreamEvent{Type: agent.EventToken, Content: "pong"}
	close(ch)
	return ch, nil
}
func (f *fakeAgent) EStop() {}

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

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/out?channel=whatsapp", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := json.Marshal(InboundPayload{
		Channel: "whatsapp", ChatKey: "+155****1234", Text: "ping",
	})
	postResp, err := http.Post(srv.URL+"/v1/in", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()

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

// ---------------------------------------------------------------------------
// bindsLoopback
// ---------------------------------------------------------------------------

func TestBindsLoopback(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:7799", true},
		{"[::1]:7799", true},
		{"localhost:7799", true},
		{"0.0.0.0:7799", false},
		{"192.168.1.1:7799", false},
		{"10.0.0.1:7799", false},
		{"", false},
		{":7799", false}, // empty host → binds all
		{"host.docker.internal:7799", false},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			if got := bindsLoopback(tc.addr); got != tc.want {
				t.Errorf("bindsLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// authorized
// ---------------------------------------------------------------------------

func TestAuthorized_NoToken(t *testing.T) {
	b := New(nil, "", "")
	if !b.authorized(httptest.NewRequest("GET", "/", nil)) {
		t.Error("empty token should allow all")
	}
}

func TestAuthorized_BadHeader(t *testing.T) {
	b := New(nil, "secret", "")
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic secret")
	if b.authorized(req) {
		t.Error("Basic auth should be rejected")
	}
}

func TestAuthorized_NoHeader(t *testing.T) {
	b := New(nil, "secret", "")
	if b.authorized(httptest.NewRequest("GET", "/", nil)) {
		t.Error("no auth header should be rejected when token set")
	}
}

// ---------------------------------------------------------------------------
// handleIn edge cases
// ---------------------------------------------------------------------------

func TestHandleIn_MethodNotAllowed(t *testing.T) {
	b := New(nil, "", "")
	w := httptest.NewRecorder()
	b.handleIn(w, httptest.NewRequest("GET", "/v1/in", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleIn_BadJSON(t *testing.T) {
	b := New(nil, "", "")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/in", strings.NewReader("not json"))
	b.handleIn(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleIn_MissingChannel(t *testing.T) {
	b := New(nil, "", "")
	body, _ := json.Marshal(InboundPayload{Text: "hi"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/in", bytes.NewReader(body))
	b.handleIn(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing channel, got %d", w.Code)
	}
}

func TestHandleIn_MissingChatKey(t *testing.T) {
	b := New(nil, "", "")
	body, _ := json.Marshal(InboundPayload{Channel: "wa", Text: "hi"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/in", bytes.NewReader(body))
	b.handleIn(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing chat, got %d", w.Code)
	}
}

func TestHandleIn_NoTextOrImages(t *testing.T) {
	b := New(nil, "", "")
	body, _ := json.Marshal(InboundPayload{Channel: "wa", ChatKey: "k"})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/in", bytes.NewReader(body))
	b.handleIn(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty text+images, got %d", w.Code)
	}
}

func TestHandleIn_BadBase64Image(t *testing.T) {
	b := New(nil, "", "")
	body, _ := json.Marshal(InboundPayload{
		Channel: "wa", ChatKey: "k",
		Images: []InboundImageB64{{Data: "!!!not-base64!!!"}},
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/in", bytes.NewReader(body))
	b.handleIn(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad base64, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// handleOut edge cases
// ---------------------------------------------------------------------------

func TestHandleOut_Unauthorized(t *testing.T) {
	b := New(nil, "secret", "")
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/out?channel=x", nil)
	b.handleOut(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleOut_MissingChannel(t *testing.T) {
	b := New(nil, "", "")
	w := httptest.NewRecorder()
	b.handleOut(w, httptest.NewRequest("GET", "/v1/out", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// health
// ---------------------------------------------------------------------------

func TestHealthEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// subscribe / unsubscribe / emit
// ---------------------------------------------------------------------------

func TestSubscribeUnsubscribe(t *testing.T) {
	b := New(nil, "", "")
	id, ch := b.subscribe("test-channel")
	if id != 1 {
		t.Errorf("first id should be 1, got %d", id)
	}

	b.mu.Lock()
	if len(b.subs["test-channel"]) != 1 {
		t.Errorf("expected 1 sub, got %d", len(b.subs["test-channel"]))
	}
	b.mu.Unlock()

	b.unsubscribe("test-channel", id)

	// Channel should be closed
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("channel should be closed after unsubscribe")
		}
	default:
		t.Error("channel should be closed, not blocking")
	}
}

func TestEmitDeliversToSubscribers(t *testing.T) {
	b := New(nil, "", "")
	id, ch := b.subscribe("bridge:test")

	frame := OutboundFrame{Channel: "bridge:test", Kind: "post", Text: "hello"}
	b.emit(frame)

	select {
	case got := <-ch:
		if got.Text != "hello" {
			t.Errorf("text = %q", got.Text)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for emitted frame")
	}

	b.unsubscribe("bridge:test", id)
}

func TestEmitDropsSlowSubscriber(t *testing.T) {
	b := New(nil, "", "")
	// Create a subscriber with channel buffer 0
	id := b.subSeq.Add(1)
	ch := make(chan OutboundFrame) // unbuffered
	b.mu.Lock()
	if b.subs["bridge:slow"] == nil {
		b.subs["bridge:slow"] = map[int64]chan OutboundFrame{}
	}
	b.subs["bridge:slow"][id] = ch
	b.mu.Unlock()

	// Emit should not block — it drops
	done := make(chan struct{})
	go func() {
		b.emit(OutboundFrame{Channel: "bridge:slow", Kind: "post", Text: "drop"})
		close(done)
	}()

	select {
	case <-done:
		// emit returned without blocking — frame was dropped
	case <-time.After(500 * time.Millisecond):
		t.Error("emit blocked on slow subscriber")
	}
}

// ---------------------------------------------------------------------------
// bridgeReply methods
// ---------------------------------------------------------------------------

func TestBridgeReply_PostInitial(t *testing.T) {
	b := New(nil, "", "")
	_, subCh := b.subscribe("bridge:whatsapp")
	rep := &bridgeReply{bridge: b, channel: "bridge:whatsapp", chatKey: "k"}

	h, err := rep.PostInitial(context.Background(), gateway.Inbound{}, "hello")
	if err != nil {
		t.Fatalf("PostInitial: %v", err)
	}
	if h == "" {
		t.Error("handle should not be empty")
	}

	select {
	case frame := <-subCh:
		if frame.Kind != "post" || frame.Text != "hello" {
			t.Errorf("frame = %+v", frame)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out")
	}
}

func TestBridgeReply_Update(t *testing.T) {
	b := New(nil, "", "")
	_, subCh := b.subscribe("bridge:wa")
	rep := &bridgeReply{bridge: b, channel: "bridge:wa", chatKey: "k"}

	rep.Update(context.Background(), "h1", "updated text")

	select {
	case frame := <-subCh:
		if frame.Kind != "update" || frame.Text != "updated text" {
			t.Errorf("frame = %+v", frame)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out")
	}
}

func TestBridgeReply_Final(t *testing.T) {
	b := New(nil, "", "")
	_, subCh := b.subscribe("bridge:wa")
	rep := &bridgeReply{bridge: b, channel: "bridge:wa", chatKey: "k"}

	rep.Final(context.Background(), "h1", "done")

	select {
	case frame := <-subCh:
		if frame.Kind != "final" || frame.Text != "done" {
			t.Errorf("frame = %+v", frame)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out")
	}
}

func TestBridgeReply_Error(t *testing.T) {
	b := New(nil, "", "")
	_, subCh := b.subscribe("bridge:wa")
	rep := &bridgeReply{bridge: b, channel: "bridge:wa", chatKey: "k"}

	rep.Error(context.Background(), "h1", fmt.Errorf("boom"))

	select {
	case frame := <-subCh:
		if frame.Kind != "error" || frame.Text != "boom" {
			t.Errorf("frame = %+v", frame)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out")
	}
}

// ---------------------------------------------------------------------------
// handleApprovalCommand
// ---------------------------------------------------------------------------

type fakeSuspender struct {
	lastCallID   string
	lastAllow    bool
	lastApprover string
	err          error
}

func (f *fakeSuspender) ResumeApproval(callID string, allow bool, approver string) error {
	f.lastCallID = callID
	f.lastAllow = allow
	f.lastApprover = approver
	return f.err
}

func TestHandleApprovalCommand_NoSuspender(t *testing.T) {
	b := New(nil, "", "")
	p := InboundPayload{Channel: "wa", ChatKey: "k", Text: "approve abc-123"}
	w := httptest.NewRecorder()
	if b.handleApprovalCommand(w, p) {
		t.Error("should return false with no suspender")
	}
}

func TestHandleApprovalCommand_NotApproval(t *testing.T) {
	b := New(nil, "", "")
	b.SetSuspender(&fakeSuspender{})
	p := InboundPayload{Channel: "wa", ChatKey: "k", Text: "just a regular message"}
	w := httptest.NewRecorder()
	if b.handleApprovalCommand(w, p) {
		t.Error("should return false for non-approval text")
	}
}

func TestHandleApprovalCommand_Approve(t *testing.T) {
	b := New(nil, "", "")
	fs := &fakeSuspender{}
	b.SetSuspender(fs)

	p := InboundPayload{Channel: "wa", ChatKey: "k", Text: "approve abc-123", From: "alice"}
	w := httptest.NewRecorder()
	if !b.handleApprovalCommand(w, p) {
		t.Error("should return true for approve command")
	}
	if fs.lastCallID != "abc-123" || !fs.lastAllow {
		t.Errorf("expected approve abc-123, got %s allow=%v", fs.lastCallID, fs.lastAllow)
	}
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

func TestHandleApprovalCommand_Deny(t *testing.T) {
	b := New(nil, "", "")
	fs := &fakeSuspender{}
	b.SetSuspender(fs)

	p := InboundPayload{Channel: "wa", ChatKey: "k", Text: "deny abc-456", From: "bob"}
	w := httptest.NewRecorder()
	if !b.handleApprovalCommand(w, p) {
		t.Error("should return true for deny command")
	}
	if fs.lastCallID != "abc-456" || fs.lastAllow {
		t.Errorf("expected deny abc-456, got %s allow=%v", fs.lastCallID, fs.lastAllow)
	}
}

func TestHandleApprovalCommand_Error(t *testing.T) {
	b := New(nil, "", "")
	b.SetSuspender(&fakeSuspender{err: fmt.Errorf("call not found")})

	p := InboundPayload{Channel: "wa", ChatKey: "k", Text: "approve abc-fff", From: "alice"}
	w := httptest.NewRecorder()
	if !b.handleApprovalCommand(w, p) {
		t.Error("should return true even on error")
	}
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Name + SetSuspender
// ---------------------------------------------------------------------------

func TestBridgeName(t *testing.T) {
	b := New(nil, "", "")
	if b.Name() != "bridge" {
		t.Errorf("Name() = %q", b.Name())
	}
}

func TestSetSuspender(t *testing.T) {
	b := New(nil, "", "")
	var s fakeSuspender
	b.SetSuspender(&s)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.suspender == nil {
		t.Error("suspender should be set")
	}
}
