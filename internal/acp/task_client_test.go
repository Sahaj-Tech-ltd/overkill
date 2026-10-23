package acp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDurableTaskClientOperationsAndReceiptIdentity(t *testing.T) {
	const id = "durable-task"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-private" {
			t.Error("missing bound credential")
			w.WriteHeader(401)
			return
		}
		task := Task{TaskID: id, MessageID: id, SessionID: "remote-session", Status: "queued"}
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/tasks":
			var req TaskSubmitRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.SessionID != "remote-session" || req.IdempotencyKey != "stable-key" {
				t.Error("wrong durable submission")
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(202)
		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+id+"/await":
			if r.URL.Query().Get("timeoutMs") != "500" {
				t.Error("wrong bounded wait")
			}
			task.Status = "completed"
			task.Reply = "ready"
		case r.Method == "POST" && r.URL.Path == "/v1/tasks/"+id+"/cancel":
			task.Status = "cancelled"
		case r.Method == "GET" && r.URL.Path == "/v1/tasks/wrong":
			task.TaskID = "different"
		case r.Method == "GET" && r.URL.Path == "/v1/tasks/"+id:
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer server.Close()
	client := NewClient(server.URL, "fixture-private")
	task, err := client.SubmitTask(t.Context(), TaskSubmitRequest{SessionID: "remote-session", Content: "work", IdempotencyKey: "stable-key"})
	require.NoError(t, err)
	require.Equal(t, "queued", task.Status)
	task, err = client.TaskStatus(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, id, task.TaskID)
	task, err = client.AwaitTask(t.Context(), id, 500*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, "ready", task.Reply)
	task, err = client.CancelTask(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "cancelled", task.Status)
	_, err = client.TaskStatus(t.Context(), "wrong")
	require.ErrorContains(t, err, "identity")
	for _, invalid := range []string{"../sessions", "a/b", "%2f", "x?query"} {
		_, err = client.TaskStatus(t.Context(), invalid)
		require.Error(t, err)
		_, err = client.CancelTask(t.Context(), invalid)
		require.Error(t, err)
	}
	_, err = client.AwaitTask(t.Context(), id, 26*time.Second)
	require.Error(t, err)
}
func TestDurableTaskClientWaitCancellationAndErrorsAreBounded(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/await") {
			<-r.Context().Done()
			close(cancelled)
			return
		}
		w.WriteHeader(500)
		_, _ = w.Write([]byte("secret response must not become a model error"))
	}))
	defer server.Close()
	client := NewClient(server.URL, "")
	_, err := client.TaskStatus(t.Context(), "id")
	require.ErrorContains(t, err, "500")
	require.NotContains(t, err.Error(), "secret response")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = client.AwaitTask(ctx, "id", 25*time.Second)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("observer cancellation did not close HTTP request")
	}
}
