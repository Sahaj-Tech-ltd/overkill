package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCanvasSharedRendererBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	_, dependencyErr := os.Stat("../../desktop/node_modules/playwright/package.json")
	if err != nil || dependencyErr != nil {
		if os.Getenv("OVERKILL_REQUIRE_FRONTEND_TEST") == "1" {
			t.Fatalf("canvas browser requires Node and desktop Playwright: %v %v", err, dependencyErr)
		}
		t.Skip("browser dependencies unavailable")
	}
	const token = "canvas-renderer-fixture-secret"
	srv := NewServer(Config{Token: token})
	handler := srv.Handler()
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fixture/canvas-event" {
			handler.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "fixture auth", 401)
			return
		}
		var body struct {
			SessionID string `json:"sessionId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "fixture body", 400)
			return
		}
		srv.bus.publish(body.SessionID, wsEvent{Type: "a2ui", SessionID: body.SessionID, Content: `{"action":"surfaceUpdate"}`})
		w.WriteHeader(204)
	})
	server := httptest.NewServer(wrapped)
	defer server.Close()
	script, err := filepath.Abs("testdata/canvas-renderer.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Env = append(os.Environ(), "OVERKILL_WEB_TEST_URL="+server.URL, "OVERKILL_WEB_TEST_TOKEN="+token)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("canvas browser: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "stale sessions and reconnect passed") {
		t.Fatalf("missing browser evidence: %s", output)
	}
}
