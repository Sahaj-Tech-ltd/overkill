package share

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

func newSession() *session.Session {
	s := session.NewSession("/tmp/x")
	s.Title = "test"
	s.Model = "claude"
	s.Provider = "anthropic"
	s.Messages = []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi <world>"},
	}
	return s
}

func TestRenderProducesValidHTML(t *testing.T) {
	t.Parallel()
	out, err := Render(newSession())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "<!doctype html>") {
		t.Fatalf("missing doctype")
	}
	if !strings.Contains(out, "hi &lt;world&gt;") {
		t.Fatalf("content not escaped: %s", out)
	}
	// Verify it actually parses as HTML.
	if _, err := xhtml.Parse(strings.NewReader(out)); err != nil {
		t.Fatalf("html parse: %v", err)
	}
}

func TestGistUploaderPayload(t *testing.T) {
	t.Parallel()
	var got gistPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing/wrong auth header")
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"html_url": "https://gist.example/abc",
		})
	}))
	defer srv.Close()

	g := &gistUploader{token: "test-token", endpoint: srv.URL, client: srv.Client()}
	url, err := g.Upload(context.Background(), "<p>hi</p>")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if url != "https://gist.example/abc" {
		t.Fatalf("wrong url: %s", url)
	}
	if got.Files["session.html"].Content != "<p>hi</p>" {
		t.Fatalf("content payload wrong")
	}
	if got.Public {
		t.Fatalf("expected private gist")
	}
}

func TestNewUploaderDefaults(t *testing.T) {
	t.Parallel()
	u, err := NewUploader(config.ShareConfig{})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if u.Name() != "transfer-sh" {
		t.Fatalf("expected transfer-sh got %s", u.Name())
	}
	u, err = NewUploader(config.ShareConfig{GitHubToken: "tk"})
	if err != nil {
		t.Fatalf("gist default: %v", err)
	}
	if u.Name() != "gist" {
		t.Fatalf("expected gist got %s", u.Name())
	}
}
