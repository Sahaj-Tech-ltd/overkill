package browser

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const testHTML = `<!doctype html>
<html><head><title>Overkill Test Page</title></head>
<body>
<h1 id="hdr">Hello Overkill</h1>
<p class="lead">A simple page for the browser test.</p>
<form id="f" method="get" action="/submit">
  <input id="name" name="name" value="">
  <select id="role" name="role">
    <option value="dev">Dev</option>
    <option value="ops">Ops</option>
  </select>
  <button id="go" type="submit">Go</button>
</form>
</body></html>`

func TestBrowserEndToEnd(t *testing.T) {
	if os.Getenv("BROWSER_TESTS") != "1" {
		t.Skip("BROWSER_TESTS=1 not set; skipping live Chrome test")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/submit") {
			fmt.Fprintf(w, "<html><head><title>Submitted</title></head><body><p id='out'>name=%s role=%s</p></body></html>",
				r.URL.Query().Get("name"), r.URL.Query().Get("role"))
			return
		}
		_, _ = w.Write([]byte(testHTML))
	}))
	defer srv.Close()

	mgr := NewManager(Options{Headless: true})
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	b, err := mgr.Get(ctx)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := b.Navigate(srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	title, err := b.Title()
	if err != nil || title != "Overkill Test Page" {
		t.Fatalf("title=%q err=%v", title, err)
	}

	png, err := b.Screenshot(800, 600)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("screenshot is not a PNG (first bytes: %x)", png[:8])
	}

	text, err := b.Text("#hdr")
	if err != nil || !strings.Contains(text, "Hello Overkill") {
		t.Fatalf("text=%q err=%v", text, err)
	}

	if err := b.Fill("#name", "harsh"); err != nil {
		t.Fatalf("fill: %v", err)
	}
	if err := b.Select("#role", "ops"); err != nil {
		t.Fatalf("select: %v", err)
	}
	if err := b.Click("#go"); err != nil {
		t.Fatalf("click: %v", err)
	}
	// Wait for the submit page.
	if err := b.WaitForSelector("#out", 10*time.Second); err != nil {
		t.Fatalf("wait: %v", err)
	}
	out, err := b.Text("#out")
	if err != nil || !strings.Contains(out, "name=harsh") || !strings.Contains(out, "role=ops") {
		t.Fatalf("submit text=%q err=%v", out, err)
	}
}
