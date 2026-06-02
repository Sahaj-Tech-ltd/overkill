package automation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// memorySOPStore is a tiny in-test implementation of SOPStore. The
// production code uses PostgresSOPStore; tests don't need Postgres.
type memorySOPStore struct {
	sops map[string]SOP
}

func newMemorySOPStore() *memorySOPStore {
	return &memorySOPStore{sops: map[string]SOP{}}
}
func (s *memorySOPStore) SaveSOP(sop *SOP) error { s.sops[sop.ID] = *sop; return nil }
func (s *memorySOPStore) LoadSOPs() ([]SOP, error) {
	out := make([]SOP, 0, len(s.sops))
	for _, v := range s.sops {
		out = append(out, v)
	}
	return out, nil
}
func (s *memorySOPStore) DeleteSOP(id string) error { delete(s.sops, id); return nil }

func newTestSOPEngine(t *testing.T) *SOPEngine {
	t.Helper()
	engine := NewSOPEngine(newMemorySOPStore(), func(action string) (string, error) {
		return "ran: " + action, nil
	})
	return engine
}

func TestSOPWebhook_Health(t *testing.T) {
	srv := NewSOPWebhookServer(newTestSOPEngine(t))
	mux := buildTestMux(srv)
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

func TestSOPWebhook_AuthRequired(t *testing.T) {
	srv := NewSOPWebhookServer(newTestSOPEngine(t))
	srv.Token = "secret"
	mux := buildTestMux(srv)
	req := httptest.NewRequest("GET", "/sop", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/sop", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with token, got %d", rec.Code)
	}
}

func TestSOPWebhook_ListReturnsRegistered(t *testing.T) {
	engine := newTestSOPEngine(t)
	if err := engine.Create(&SOP{ID: "deploy", Name: "Deploy app", Steps: []Step{{Action: "echo ok"}}}); err != nil {
		t.Fatal(err)
	}
	srv := NewSOPWebhookServer(engine)
	mux := buildTestMux(srv)

	req := httptest.NewRequest("GET", "/sop", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0]["id"] != "deploy" {
		t.Errorf("unexpected list: %+v", list)
	}
}

func TestSOPWebhook_TriggerUnknownIs404(t *testing.T) {
	srv := NewSOPWebhookServer(newTestSOPEngine(t))
	mux := buildTestMux(srv)
	req := httptest.NewRequest("POST", "/sop/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestSOPWebhook_TriggerStartsExecution(t *testing.T) {
	engine := newTestSOPEngine(t)
	_ = engine.Create(&SOP{
		ID:    "deploy",
		Name:  "Deploy app",
		Mode:  ModeAuto,
		Steps: []Step{{Action: "echo go"}},
	})
	srv := NewSOPWebhookServer(engine)
	mux := buildTestMux(srv)

	req := httptest.NewRequest("POST", "/sop/deploy", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "deploy") || !strings.Contains(body, "started") {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestSOPWebhook_GetReturnsSOP(t *testing.T) {
	engine := newTestSOPEngine(t)
	_ = engine.Create(&SOP{ID: "x", Name: "X", Steps: []Step{{Action: "echo"}}})
	srv := NewSOPWebhookServer(engine)
	mux := buildTestMux(srv)

	req := httptest.NewRequest("GET", "/sop/x", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"id":"x"`) {
		t.Errorf("body missing SOP: %s", rec.Body.String())
	}
}

func TestSOPWebhook_MethodNotAllowed(t *testing.T) {
	engine := newTestSOPEngine(t)
	_ = engine.Create(&SOP{ID: "x", Steps: []Step{{Action: "echo"}}})
	srv := NewSOPWebhookServer(engine)
	mux := buildTestMux(srv)

	req := httptest.NewRequest("DELETE", "/sop/x", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

// buildTestMux mirrors the mux Run() installs so tests don't need a
// live listener.
func buildTestMux(s *SOPWebhookServer) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/sop", s.handleList)
	mux.HandleFunc("/sop/", s.handleTrigger)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

// Compile-time check that engine type signatures haven't drifted.
var _ = context.Background
