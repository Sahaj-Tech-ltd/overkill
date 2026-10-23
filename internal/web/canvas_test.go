package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func canvasRequest(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	out := httptest.NewRecorder()
	srv.Handler().ServeHTTP(out, req)
	return out
}

func TestCanvasPushValidatesBeforePublishing(t *testing.T) {
	invalid := []struct{ name, line, surface string }{
		{"multiple actions", `{"createSurface":{"surfaceId":"view"},"deleteSurface":{"surfaceId":"view"}}`, ""},
		{"extra unknown action", `{"deleteSurface":{"surfaceId":"view"},"other":{}}`, ""},
		{"unknown action", `{"other":{"surfaceId":"view"}}`, ""},
		{"duplicate action", `{"deleteSurface":{"surfaceId":"view"},"deleteSurface":{"surfaceId":"view"}}`, ""},
		{"escaped duplicate", `{"deleteSurface":{"surfaceId":"view","surface\u0049d":"elsewhere"}}`, ""},
		{"nested duplicate", `{"surfaceUpdate":{"surfaceId":"view","components":[{"id":"one","id":"two"}]}}`, ""},
		{"trailing JSON", `{"deleteSurface":{"surfaceId":"view"}} {}`, ""},
		{"null event", `null`, ""},
		{"array event", `[]`, ""},
		{"null payload", `{"deleteSurface":null}`, ""},
		{"array payload", `{"deleteSurface":[]}`, ""},
		{"missing surface", `{"deleteSurface":{}}`, ""},
		{"nonstring surface", `{"deleteSurface":{"surfaceId":12}}`, ""},
		{"blank surface", `{"deleteSurface":{"surfaceId":" "}}`, ""},
		{"control surface", `{"deleteSurface":{"surfaceId":"x\ny"}}`, ""},
		{"surface mismatch", `{"deleteSurface":{"surfaceId":"view"}}`, "other"},
		{"session mismatch", `{"deleteSurface":{"surfaceId":"view","sessionId":"other"}}`, ""},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(Config{NoAuth: true})
			sub := srv.bus.subscribe()
			defer srv.bus.unsubscribe(sub)
			body, _ := json.Marshal(map[string]string{"sessionId": "session", "surfaceId": tc.surface, "jsonl": tc.line})
			out := canvasRequest(t, srv, "/api/canvas/push", string(body))
			if out.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", out.Code, out.Body)
			}
			select {
			case ev := <-sub.ch:
				t.Fatalf("invalid event published: %+v", ev)
			default:
			}
		})
	}
}

func TestCanvasMissingStorageNeverPublishes(t *testing.T) {
	srv := NewServer(Config{NoAuth: true, Agent: &fakeAgent{sessionID: "current"}})
	sub := srv.bus.subscribe()
	defer srv.bus.unsubscribe(sub)
	line := `{"createSurface":{"surfaceId":"view","catalog":"overkill.canvas.v1"}}`
	body, _ := json.Marshal(map[string]string{"jsonl": line})
	out := canvasRequest(t, srv, "/api/canvas/push", string(body))
	if out.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing storage status %d: %s", out.Code, out.Body)
	}
	select {
	case ev := <-sub.ch:
		t.Fatalf("uncommitted event published: %+v", ev)
	default:
	}
}

func TestCanvasRequestEnvelopeAndReset(t *testing.T) {
	for _, path := range []string{"/api/canvas/push", "/api/canvas/reset"} {
		for _, body := range []string{`null`, `[]`, `{"sessionId":"s","sessionId":"other"}`, `{"sessionId":"s"} {}`, `{"sessionId":"s","extra":true}`, `{"sessionId":"s","SessionId":"other"}`, `{"sessionId":null}`, `{"sessionId":" "}`} {
			srv := NewServer(Config{NoAuth: true})
			sub := srv.bus.subscribe()
			out := canvasRequest(t, srv, path, body)
			if out.Code != http.StatusBadRequest {
				t.Errorf("%s %s: status %d", path, body, out.Code)
			}
			select {
			case ev := <-sub.ch:
				t.Errorf("invalid request published: %+v", ev)
			default:
			}
			srv.bus.unsubscribe(sub)
		}
	}
	srv := NewServer(Config{NoAuth: true, Agent: &fakeAgent{sessionID: "current"}})
	sub := srv.bus.subscribe()
	defer srv.bus.unsubscribe(sub)
	out := canvasRequest(t, srv, "/api/canvas/reset", `{}`)
	if out.Code != http.StatusServiceUnavailable {
		t.Fatalf("reset without storage: %d", out.Code)
	}
	select {
	case ev := <-sub.ch:
		t.Fatalf("uncommitted reset published: %+v", ev)
	default:
	}
	missing := canvasRequest(t, NewServer(Config{NoAuth: true}), "/api/canvas/reset", `{}`)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("unscoped reset accepted: %d", missing.Code)
	}
}

func TestCanvasSnapshotHonestAndAuthenticated(t *testing.T) {
	srv := NewServer(Config{Token: "secret"})
	for _, tc := range []struct {
		method, token string
		want          int
	}{
		{http.MethodGet, "", http.StatusUnauthorized},
		{http.MethodPost, "secret", http.StatusMethodNotAllowed},
		{http.MethodGet, "secret", http.StatusServiceUnavailable},
	} {
		req := httptest.NewRequest(tc.method, "/api/canvas/snapshot?sessionId=s&surfaceId=v", nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		out := httptest.NewRecorder()
		srv.Handler().ServeHTTP(out, req)
		if out.Code != tc.want {
			t.Fatalf("status %d want %d: %s", out.Code, tc.want, out.Body)
		}

	}
}

func TestCanvasMutationRoutesRequireAuthAndPost(t *testing.T) {
	srv := NewServer(Config{Token: "secret"})
	for _, path := range []string{"/api/canvas/push", "/api/canvas/reset"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"sessionId":"session"}`))
		out := httptest.NewRecorder()
		srv.Handler().ServeHTTP(out, req)
		if out.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated status %d", path, out.Code)
		}
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		out = httptest.NewRecorder()
		srv.Handler().ServeHTTP(out, req)
		if out.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s method status %d", path, out.Code)
		}
	}
}
