package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestClientNonStreamBodyDeadlines(t *testing.T) {
	calls := map[string]func(context.Context, *Client) error{
		"info":     func(ctx context.Context, c *Client) error { _, err := c.GetInfo(ctx); return err },
		"sessions": func(ctx context.Context, c *Client) error { _, err := c.ListSessions(ctx); return err },
		"send":     func(ctx context.Context, c *Client) error { _, err := c.Send(ctx, "hello"); return err },
		"cancel":   func(ctx context.Context, c *Client) error { return c.Cancel(ctx, "message") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			require.ErrorIs(t, call(ctx, NewClient(server.URL, "")), context.DeadlineExceeded)
		})
	}
}

func TestClientPreservesCustomTransportAndBoundsDefaultBodyReads(t *testing.T) {
	var requests []*http.Request
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r)
		deadline, ok := r.Context().Deadline()
		if strings.HasSuffix(r.URL.Path, "/events") {
			require.False(t, ok, "stream must follow caller lifetime")
		} else {
			require.True(t, ok)
			require.InDelta(t, DefaultRequestTimeout.Seconds(), time.Until(deadline).Seconds(), 1)
		}
		require.Equal(t, "Bearer fixture", r.Header.Get("Authorization"))
		body := `{}`
		if r.URL.Path == "/v1/sessions" {
			body = `[]`
		}
		if r.URL.Path == "/v1/messages" {
			body = `{"messageID":"fixture"}`
		}
		if strings.HasSuffix(r.URL.Path, "/events") {
			body = "data: {\"type\":\"done\"}\n\n"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	client := NewClient("http://transport.invalid", "fixture")
	client.HTTP = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("custom redirect policy") }}
	_, err := client.GetInfo(t.Context())
	require.NoError(t, err)
	_, err = client.ListSessions(t.Context())
	require.NoError(t, err)
	require.NoError(t, client.Cancel(t.Context(), "id"))
	stream, err := client.Send(t.Context(), "hello")
	require.NoError(t, err)
	for event := range stream {
		require.Equal(t, "done", event.Type)
	}
	require.Len(t, requests, 5)
	for _, request := range requests {
		require.ErrorIs(t, request.Context().Err(), context.Canceled)
	}
	require.NotNil(t, client.HTTP.CheckRedirect)
}

func TestStreamMalformedDroppedAndOversizedFramesFailOnce(t *testing.T) {
	cases := map[string]string{
		"truncated HTTP body": "data: {\"type\":\"text_delta\",\"content\":\"partial\"}\n\n",
		"empty":               "",
		"dropped after text":  "data: {\"type\":\"text_delta\",\"content\":\"partial\"}\n\n",
		"bad JSON":            "data: not JSON\n\n",
		"missing type":        "data: {}\n\n",
		"unknown type":        "data: {\"type\":\"unknown\"}\n\n",
		"truncated terminal":  "data: {\"type\":\"done\"}",
		"oversized line":      "data: " + strings.Repeat("x", maxEventBytes+1) + "\n\n",
		"oversized multiline": strings.Repeat("data: "+strings.Repeat("x", 1024)+"\n", 1025) + "\n",
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if name == "truncated HTTP body" {
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)+100))
				}
				_, _ = io.WriteString(w, payload)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			events, err := NewClient(server.URL, "").Stream(ctx, "message")
			require.NoError(t, err)
			terminals := 0
			for event := range events {
				if event.Type == "done" || event.Type == "error" {
					terminals++
					require.Equal(t, "error", event.Type)
					require.NotEmpty(t, event.Error)
				}
			}
			require.Equal(t, 1, terminals)
		})
	}
}

func TestStreamSupportsMultilineSSEAndStopsAtTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, ": heartbeat\n\ndata:{\"type\":\"text_delta\",\ndata: \"content\":\"hello\"}\n\ndata: {\"type\":\"done\"}\n\ndata: malformed after terminal\n\n")
	}))
	defer server.Close()
	events, err := NewClient(server.URL, "").Stream(t.Context(), "message")
	require.NoError(t, err)
	var received []Event
	for event := range events {
		received = append(received, event)
	}
	require.Len(t, received, 2)
	require.Equal(t, "hello", received[0].Content)
	require.Equal(t, "done", received[1].Type)
}

func TestStreamCancelAbandonedReceiverReleasesConnection(t *testing.T) {
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(disconnected)
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 256; i++ {
			if _, err := fmt.Fprint(w, "data: {\"type\":\"text_delta\"}\n\n"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	events, err := NewClient(server.URL, "").Stream(ctx, "message")
	require.NoError(t, err)
	// Wait for the bounded buffer to fill without consuming it.
	require.Eventually(t, func() bool { return len(events) == cap(events) }, time.Second, time.Millisecond)
	cancel()
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("abandoned stream retained HTTP connection")
	}
	terminals := 0
	for event := range events {
		if event.Type == "error" {
			terminals++
		}
	}
	require.Equal(t, 1, terminals)
}

func TestClientCustomBodyClosedOnContextCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	client := NewClient("http://transport.invalid", "")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: reader, Header: make(http.Header)}, nil
	})}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := client.GetInfo(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestStreamHeaderReadHonorsCallerDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := NewClient(server.URL, "").Stream(ctx, "message")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
