package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetUpdates_ParsesMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": []map[string]any{{
				"update_id": 100,
				"message": map[string]any{
					"message_id": 7,
					"text":       "hi",
					"from":       map[string]any{"id": 1, "username": "alice"},
					"chat":       map[string]any{"id": 42, "type": "private"},
				},
			}},
		})
	}))
	defer srv.Close()

	c := New("TEST")
	c.BaseURL = srv.URL
	updates, err := c.GetUpdates(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || updates[0].Message.Text != "hi" {
		t.Fatalf("bad updates: %+v", updates)
	}
	if updates[0].Message.Chat.ID != 42 {
		t.Fatalf("chat id = %d want 42", updates[0].Message.Chat.ID)
	}
}

func TestEditMessage_NotModifiedIsOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":          false,
			"description": "Bad Request: message is not modified",
		})
	}))
	defer srv.Close()

	c := New("TEST")
	c.BaseURL = srv.URL
	if err := c.EditMessageText(context.Background(), 1, 1, "x"); err != nil {
		t.Fatalf("not-modified should be swallowed, got %v", err)
	}
}
