package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	SetAuthDir(dir)
	defer SetAuthDir("")

	tok := &Token{
		AccessToken: "abc123",
		Provider:    "anthropic",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(time.Hour).UTC().Truncate(time.Second),
	}
	if err := SaveToken(tok); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadToken("anthropic")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || got.AccessToken != "abc123" {
		t.Fatalf("round-trip failed: %+v", got)
	}

	// Verify file mode 0600
	info, err := os.Stat(filepath.Join(dir, "anthropic.json"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %o, want 0600", mode)
	}

	if err := DeleteToken("anthropic"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got2, _ := LoadToken("anthropic")
	if got2 != nil {
		t.Errorf("token still present after delete")
	}
}

func TestLoadTokenMissing(t *testing.T) {
	dir := t.TempDir()
	SetAuthDir(dir)
	defer SetAuthDir("")
	tok, err := LoadToken("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if tok != nil {
		t.Errorf("expected nil token, got %+v", tok)
	}
}

func TestDeviceFlowHappyPath(t *testing.T) {
	var pollCount int32

	mux := http.NewServeMux()
	mux.HandleFunc("/device/code", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"device_code":"DEV123","user_code":"USER-CODE","verification_uri":"https://example.com/verify","expires_in":600,"interval":1}`))
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&pollCount, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 2 {
			w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		w.Write([]byte(`{"access_token":"TOK-XYZ","token_type":"Bearer","expires_in":3600}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	SetEndpointOverride("anthropic", providerConfig{
		ClientID:  "test-client",
		DeviceURL: srv.URL + "/device/code",
		TokenURL:  srv.URL + "/token",
		Scope:     "test",
		GrantType: "urn:ietf:params:oauth:grant-type:device_code",
	})
	defer ClearEndpointOverride("anthropic")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	flow, err := StartDeviceFlow(ctx, "anthropic")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if flow.UserCode != "USER-CODE" {
		t.Errorf("user code = %q", flow.UserCode)
	}
	if flow.VerificationURL != "https://example.com/verify" {
		t.Errorf("verify url = %q", flow.VerificationURL)
	}
	tok, err := PollForToken(ctx, flow)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if tok.AccessToken != "TOK-XYZ" {
		t.Errorf("access token = %q", tok.AccessToken)
	}
	if tok.Provider != "anthropic" {
		t.Errorf("provider = %q", tok.Provider)
	}
}

func TestDeviceFlowDenied(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/device/code", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"device_code":"D","user_code":"U","verification_uri":"x","expires_in":60,"interval":1}`))
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error":"access_denied"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	SetEndpointOverride("anthropic", providerConfig{
		ClientID:  "x",
		DeviceURL: srv.URL + "/device/code",
		TokenURL:  srv.URL + "/token",
		Scope:     "x",
		GrantType: "urn:ietf:params:oauth:grant-type:device_code",
	})
	defer ClearEndpointOverride("anthropic")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	flow, err := StartDeviceFlow(ctx, "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	_, err = PollForToken(ctx, flow)
	if err == nil {
		t.Fatal("expected denied error")
	}
}
