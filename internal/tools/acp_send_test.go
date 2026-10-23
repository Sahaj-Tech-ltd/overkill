package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/stretchr/testify/require"
)

func TestACPSendRequiresTerminalAndForwardsSession(t *testing.T) {
	for _, tt := range []struct{ name, payload, wantError string }{
		{"done", "data: {\"type\":\"text_delta\",\"content\":\"hello\"}\n\ndata: {\"type\":\"done\"}\n\n", ""},
		{"dropped", "data: {\"type\":\"text_delta\",\"content\":\"partial\"}\n\n", "unexpected EOF"},
		{"malformed", "data: not-json\n\n", "invalid SSE JSON"},
		{"remote error", "data: {\"type\":\"error\",\"error\":\"worker failed\"}\n\n", "worker failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			posted := make(chan acp.SendRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				switch r.URL.Path {
				case "/v1/messages":
					var message acp.SendRequest
					if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
						w.WriteHeader(400)
						return
					}
					posted <- message
					_, _ = io.WriteString(w, `{"messageID":"fixture-message"}`)
				case "/v1/messages/fixture-message/events":
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, tt.payload)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			input, err := json.Marshal(acpSendInput{AgentURL: server.URL, Token: "fixture-token", Content: "frontend needs backend update", Session: "backend-session"})
			require.NoError(t, err)
			output, err := NewACPSendTool().Execute(t.Context(), input)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				require.Nil(t, output)
			} else {
				require.NoError(t, err)
				var reply map[string]string
				require.NoError(t, json.Unmarshal(output, &reply))
				require.Equal(t, "hello", reply["reply"])
			}
			message := <-posted
			require.Equal(t, "backend-session", message.SessionID)
			require.Equal(t, "frontend needs backend update", message.Content)
		})
	}
}

func TestACPSendReturnsCallerDeadlineForHungStream(t *testing.T) {
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			_, _ = io.WriteString(w, `{"messageID":"message"}`)
			return
		}
		defer close(disconnected)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	input, err := json.Marshal(acpSendInput{AgentURL: server.URL, Content: "long work"})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	output, err := NewACPSendTool().Execute(ctx, input)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, output)
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("tool cancellation retained remote stream")
	}
}
