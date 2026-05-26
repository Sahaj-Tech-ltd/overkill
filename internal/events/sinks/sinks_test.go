package sinks

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/events"
)

func sampleEvent() events.CompletionEvent {
	return events.CompletionEvent{
		SessionID:  "test-sess",
		Intent:     "fix the build",
		Outcome:    "success",
		Artefacts:  []events.Artefact{{Kind: "file", Ref: "main.go"}},
		DurationMs: 500,
		CostUSD:    0.001,
		EmittedAt:  time.Now(),
	}
}

// --- LogSink ---

func TestLogSink_WritesValidJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	sink := NewLogSink(logger)

	evt := sampleEvent()
	if err := sink.Send(context.Background(), evt); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	line := buf.String()
	if line == "" {
		t.Fatal("expected log output, got empty string")
	}
	// Strip trailing newline from log.Println.
	line = line[:len(line)-1]

	var got events.CompletionEvent
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, line)
	}
	if got.SessionID != evt.SessionID {
		t.Errorf("session_id: got %q, want %q", got.SessionID, evt.SessionID)
	}
	if got.Outcome != evt.Outcome {
		t.Errorf("outcome: got %q, want %q", got.Outcome, evt.Outcome)
	}
}

func TestLogSink_Name(t *testing.T) {
	s := NewLogSink(log.Default())
	if s.Name() != "log" {
		t.Errorf("Name() = %q, want %q", s.Name(), "log")
	}
}

// --- GatewaySink ---

func TestGatewaySink_CallsSendFunc(t *testing.T) {
	var gotChannel, gotChatKey, gotText string
	sendFn := func(channel, chatKey, text string) error {
		gotChannel = channel
		gotChatKey = chatKey
		gotText = text
		return nil
	}

	sink := NewGatewaySink("bridge:whatsapp", "chat-42", sendFn)
	if err := sink.Send(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if gotChannel != "bridge:whatsapp" {
		t.Errorf("channel: got %q, want %q", gotChannel, "bridge:whatsapp")
	}
	if gotChatKey != "chat-42" {
		t.Errorf("chatKey: got %q, want %q", gotChatKey, "chat-42")
	}
	if gotText == "" {
		t.Error("expected non-empty text from GatewaySink")
	}
}

func TestGatewaySink_Name(t *testing.T) {
	s := NewGatewaySink("", "", func(_, _, _ string) error { return nil })
	if s.Name() != "gateway" {
		t.Errorf("Name() = %q, want %q", s.Name(), "gateway")
	}
}

// --- WebhookSink ---

func TestWebhookSink_Name(t *testing.T) {
	s := NewWebhookSink("http://example.com", "token")
	if s.Name() != "webhook" {
		t.Errorf("Name() = %q, want %q", s.Name(), "webhook")
	}
}

func TestWebhookSink_Posts200OK(t *testing.T) {
	var received events.CompletionEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewWebhookSink(srv.URL, "secret")
	evt := sampleEvent()

	if err := sink.Send(context.Background(), evt); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
	if received.SessionID != evt.SessionID {
		t.Errorf("received session_id %q, want %q", received.SessionID, evt.SessionID)
	}
}

func TestWebhookSink_RetriesOnceon5xx(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewWebhookSink(srv.URL, "")
	if err := sink.Send(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("expected success on retry, got: %v", err)
	}
	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls (initial + retry), got %d", callCount.Load())
	}
}

func TestWebhookSink_DoesNotRetryOn4xx(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	sink := NewWebhookSink(srv.URL, "")
	err := sink.Send(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
	if callCount.Load() != 1 {
		t.Errorf("expected exactly 1 call on 4xx, got %d", callCount.Load())
	}
}

func TestWebhookSink_5xxRetryFails(t *testing.T) {
	// Both attempts return 500 — Send must return an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sink := NewWebhookSink(srv.URL, "")
	err := sink.Send(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("expected error when both attempts return 5xx, got nil")
	}
}

func TestWebhookSink_SetsAuthHeader(t *testing.T) {
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := NewWebhookSink(srv.URL, "my-secret-token")
	if err := sink.Send(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("Send error: %v", err)
	}
	if authHeader != "Bearer my-secret-token" {
		t.Errorf("Authorization header: got %q, want %q", authHeader, "Bearer my-secret-token")
	}
}
