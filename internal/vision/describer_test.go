package vision

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicDescribe_Roundtrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "k" {
			t.Errorf("missing api key")
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"type":"image"`) {
			t.Errorf("request missing image block: %s", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "a login form"}},
		})
	}))
	defer srv.Close()

	d := NewAnthropic("k", "claude-x")
	d.BaseURL = srv.URL
	got, err := d.Describe(context.Background(), []Image{{Bytes: []byte("png-bytes"), Mime: "image/png"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a login form" {
		t.Fatalf("got %q want a login form", got)
	}
}

func TestAnthropicDescribe_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`rate limited`))
	}))
	defer srv.Close()
	d := NewAnthropic("k", "claude-x")
	d.BaseURL = srv.URL
	if _, err := d.Describe(context.Background(), []Image{{Bytes: []byte("x")}}, ""); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("want 429 error, got %v", err)
	}
}

func TestMIMEFromBytes(t *testing.T) {
	cases := []struct {
		bytes []byte
		want  string
	}{
		{[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0}, "image/png"},
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg"},
		{[]byte("GIF89a..."), "image/gif"},
		{append([]byte("RIFF"), append([]byte{0, 0, 0, 0}, []byte("WEBP")...)...), "image/webp"},
		{[]byte("nope"), "image/png"}, // fallback
	}
	for _, c := range cases {
		if got := MIMEFromBytes(c.bytes); got != c.want {
			t.Errorf("MIMEFromBytes(%q) = %s want %s", c.bytes, got, c.want)
		}
	}
}
