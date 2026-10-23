package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserCookieIssuanceRequiresBearerAndOrigin(t *testing.T) {
	srv := NewServer(Config{Token: "secret"})
	for _, tc := range []struct {
		method, bearer, cookie, origin string
		want                           int
	}{
		{"POST", "", "", "", 401}, {"POST", "wrong", "", "", 401}, {"POST", "wrong", "secret", "", 401}, {"POST", "", "secret", "", 401},
		{"POST", "secret", "", "http://evil.test", 403}, {"DELETE", "", "secret", "http://evil.test", 403},
		{"POST", "secret", "", "http://example.com", 204}, {"DELETE", "", "", "http://example.com", 204},
		{"GET", "secret", "", "", 405},
	} {
		r := httptest.NewRequest(tc.method, "http://example.com/api/auth", nil)
		if tc.bearer != "" {
			r.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "overkill-token", Value: tc.cookie})
		}
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%+v status %d: %s", tc, w.Code, w.Body)
		}
		if w.Code != 204 && len(w.Result().Cookies()) > 0 {
			t.Fatal("rejected request minted a cookie")
		}
		if w.Code == 204 && tc.method == "POST" {
			c := w.Result().Cookies()[0]
			if c.Value != "secret" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Secure || c.Path != "/api" || c.MaxAge != 0 {
				t.Fatalf("unsafe local session cookie: %+v", c)
			}
		}
	}
	foreign := httptest.NewRequest("GET", "http://example.com/api/events", nil)
	foreign.Header.Set("Origin", "http://evil.test")
	foreign.AddCookie(&http.Cookie{Name: "overkill-token", Value: "secret"})
	blocked := httptest.NewRecorder()
	srv.Handler().ServeHTTP(blocked, foreign)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("cross-origin cookie upgrade status %d", blocked.Code)
	}
	r := httptest.NewRequest("POST", "https://example.com/api/auth", nil)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 204 || !w.Result().Cookies()[0].Secure {
		t.Fatal("TLS cookie must be Secure")
	}
}

func TestBrowserAuthenticatedWebSocket(t *testing.T) {
	node, err := exec.LookPath("node")
	script, pathErr := filepath.Abs("testdata/browser-auth.cjs")
	_, dependencyErr := os.Stat("../../desktop/node_modules/playwright/package.json")
	if err != nil || pathErr != nil || dependencyErr != nil {
		if os.Getenv("OVERKILL_REQUIRE_FRONTEND_TEST") == "1" {
			t.Fatalf("browser auth requires Node and desktop Playwright dependencies: %v %v %v", err, pathErr, dependencyErr)
		}
		t.Skip("Node and desktop Playwright dependencies unavailable")
	}
	_, srv := newTestServer(t, "browser-auth-fixture-secret")
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Env = append(os.Environ(), "OVERKILL_WEB_TEST_URL="+srv.URL, "OVERKILL_WEB_TEST_TOKEN=browser-auth-fixture-secret")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real browser auth: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "actual WebSocket event, reload and logout passed") {
		t.Fatalf("missing browser acceptance: %s", output)
	}
}
