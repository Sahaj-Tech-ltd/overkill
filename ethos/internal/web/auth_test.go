package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTokenFromRequest(t *testing.T) {
	t.Run("query ignored", func(t *testing.T) {
		// Query-param tokens are no longer accepted to prevent token
		// leakage through access logs / browser history / referers.
		r := httptest.NewRequest("GET", "/x?t=alpha", nil)
		if got := tokenFromRequest(r); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
	t.Run("bearer", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x", nil)
		r.Header.Set("Authorization", "Bearer beta")
		if got := tokenFromRequest(r); got != "beta" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("cookie", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x", nil)
		r.AddCookie(&http.Cookie{Name: "overkill-token", Value: "gamma"})
		if got := tokenFromRequest(r); got != "gamma" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("none", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/x", nil)
		if got := tokenFromRequest(r); got != "" {
			t.Errorf("got %q", got)
		}
	})
}

func TestNoAuthBypass(t *testing.T) {
	srv := NewServer(Config{NoAuth: true, Provider: "test", Version: "test"})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	res, err := http.Get(hs.URL + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("got %d", res.StatusCode)
	}
}

func TestIndexSetsCookieFromQuery(t *testing.T) {
	srv := NewServer(Config{Token: "shh", Provider: "test", Version: "test"})
	hs := httptest.NewServer(srv.Handler())
	defer hs.Close()
	res, err := http.Get(hs.URL + "/?t=shh")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	found := false
	for _, c := range res.Cookies() {
		if c.Name == "overkill-token" && c.Value == "shh" {
			found = true
		}
	}
	if !found {
		t.Errorf("cookie not set; got %v", res.Cookies())
	}
}

func TestWSAcceptKey(t *testing.T) {
	// canonical sample from RFC6455 §1.3
	got := wsAcceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if !strings.EqualFold(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}
