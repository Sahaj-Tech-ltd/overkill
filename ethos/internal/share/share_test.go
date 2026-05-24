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
	if _, err := xhtml.Parse(strings.NewReader(out)); err != nil {
		t.Fatalf("html parse: %v", err)
	}
}

func TestRenderNilSession(t *testing.T) {
	t.Parallel()
	_, err := Render(nil)
	if err == nil {
		t.Fatal("expected error for nil session")
	}
}

func TestRenderWithToolCalls(t *testing.T) {
	t.Parallel()
	s := newSession()
	s.Messages = []providers.Message{
		{Role: "assistant", Content: "let me check",
			ToolCalls: []providers.ToolCall{
				{Name: "shell", Arguments: "ls -la"},
			},
		},
		{Role: "tool", Content: "file1 file2"},
	}
	out, err := Render(s)
	if err != nil {
		t.Fatalf("render with tool calls: %v", err)
	}
	if !strings.Contains(out, "shell") {
		t.Error("tool call name not rendered")
	}
	if !strings.Contains(out, "ls -la") {
		t.Error("tool call args not rendered")
	}
	if !strings.Contains(out, "msg-tool") {
		t.Error("tool role CSS class missing")
	}
}

func TestRenderEmptyTitle(t *testing.T) {
	t.Parallel()
	s := newSession()
	s.Title = ""
	out, err := Render(s)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "Overkill Session") {
		t.Error("fallback title missing")
	}
}

func TestRenderEmptyModelAndProvider(t *testing.T) {
	t.Parallel()
	s := newSession()
	s.Model = ""
	s.Provider = ""
	out, err := Render(s)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
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

func TestGistUploaderErrorResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"bad token"}`))
	}))
	defer srv.Close()

	g := &gistUploader{token: "bad", endpoint: srv.URL, client: srv.Client()}
	_, err := g.Upload(context.Background(), "<p>hi</p>")
	if err == nil {
		t.Fatal("expected error on 403")
	}
}

func TestGistUploaderEmptyHTMLURL(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		// No html_url in response
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "ok"})
	}))
	defer srv.Close()

	g := &gistUploader{token: "tk", endpoint: srv.URL, client: srv.Client()}
	_, err := g.Upload(context.Background(), "<p>hi</p>")
	if err == nil || !strings.Contains(err.Error(), "empty html_url") {
		t.Errorf("expected empty html_url error, got %v", err)
	}
}

func TestGistUploaderBadJSONResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	g := &gistUploader{token: "tk", endpoint: srv.URL, client: srv.Client()}
	_, err := g.Upload(context.Background(), "<p>hi</p>")
	if err == nil {
		t.Fatal("expected error on bad JSON response")
	}
}

func TestTransferShUploaderSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "<p>test</p>" {
			t.Errorf("body = %q", body)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("https://transfer.example/abc123\n"))
	}))
	defer srv.Close()

	tu := &transferShUploader{endpoint: srv.URL, client: srv.Client()}
	url, err := tu.Upload(context.Background(), "<p>test</p>")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if url != "https://transfer.example/abc123" {
		t.Errorf("url = %q", url)
	}
}

func TestTransferShUploaderErrorResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	tu := &transferShUploader{endpoint: srv.URL, client: srv.Client()}
	_, err := tu.Upload(context.Background(), "<p>hi</p>")
	if err == nil {
		t.Fatal("expected error on 429")
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

func TestNewUploaderExplicitBackends(t *testing.T) {
	t.Parallel()
	u, err := NewUploader(config.ShareConfig{Backend: "transfer-sh"})
	if err != nil {
		t.Fatalf("explicit transfer-sh: %v", err)
	}
	if u.Name() != "transfer-sh" {
		t.Errorf("name = %q", u.Name())
	}

	u, err = NewUploader(config.ShareConfig{Backend: "gist", GitHubToken: "tk"})
	if err != nil {
		t.Fatalf("explicit gist: %v", err)
	}
	if u.Name() != "gist" {
		t.Errorf("name = %q", u.Name())
	}
}

func TestNewUploaderGistNoToken(t *testing.T) {
	t.Parallel()
	_, err := NewUploader(config.ShareConfig{Backend: "gist"})
	if err == nil {
		t.Fatal("expected error for gist without token")
	}
}

func TestNewUploaderUnknownBackend(t *testing.T) {
	t.Parallel()
	_, err := NewUploader(config.ShareConfig{Backend: "s3"})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}
