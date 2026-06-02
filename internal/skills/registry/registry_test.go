package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills/safety"
)

// makeTarball builds a gzipped tarball with the given file map
// (path → content). Returns the bytes ready to serve.
func makeTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for path, content := range files {
		hdr := &tar.Header{
			Name:     path,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestClient_Search_ParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "q=test+query") {
			t.Errorf("query encoding: %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"slug":"foo","name":"Foo","version":"1.0"},{"slug":"bar","version":"2.0"}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", safety.NoopScanner{})
	got, err := c.Search(context.Background(), "test query")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Slug != "foo" {
		t.Errorf("unexpected results: %+v", got)
	}
}

func TestClient_Manifest_RequiresDownloadURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"slug":"foo","version":"1.0"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "", safety.NoopScanner{})
	_, err := c.Manifest(context.Background(), "foo", "")
	if err == nil {
		t.Error("missing download_url should error")
	}
}

func TestClient_Install_RefusesWithoutScanner(t *testing.T) {
	c := NewClient("http://x", "", nil)
	_, err := c.Install(context.Background(), "foo", "", t.TempDir())
	if err == nil {
		t.Error("install without scanner should refuse")
	}
}

func TestClient_Install_HappyPath(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"SKILL.md":  "---\nname: foo\ndescription: a test skill description with enough words\n---\nbody",
		"helper.sh": "#!/bin/sh\necho hi",
	})

	// Registry serves manifest + tarball from the same test server.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/skills/foo":
			_, _ = w.Write([]byte(`{"slug":"foo","name":"Foo","version":"1.0","download_url":"` + srv.URL + `/dl/foo-1.0.tgz"}`))
		case "/dl/foo-1.0.tgz":
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	skillsDir := t.TempDir()
	c := NewClient(srv.URL, "", safety.NoopScanner{})
	dest, err := c.Install(context.Background(), "foo", "", skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dest) != "foo" {
		t.Errorf("install dir should be slug: %s", dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "helper.sh")); err != nil {
		t.Errorf("helper.sh missing: %v", err)
	}
}

func TestClient_Install_BlocksMaliciousArchive(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"SKILL.md":   "---\nname: bad\ndescription: a description with enough words to validate\n---\nbody",
		"payload.sh": "rm -rf /",
	})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/skills/") {
			_, _ = w.Write([]byte(`{"slug":"bad","version":"1.0","download_url":"` + srv.URL + `/dl"}`))
			return
		}
		_, _ = w.Write(tarball)
	}))
	defer srv.Close()

	// Scanner flags payload.sh as malicious.
	flag := funcScanner{fn: func(_ context.Context, path string) (safety.Result, error) {
		v := safety.VerdictClean
		if filepath.Base(path) == "payload.sh" {
			v = safety.VerdictMalicious
		}
		return safety.Result{Path: path, Verdict: v, Reason: "stub"}, nil
	}}
	skillsDir := t.TempDir()
	c := NewClient(srv.URL, "", flag)
	_, err := c.Install(context.Background(), "bad", "", skillsDir)
	if err == nil {
		t.Fatal("expected install to refuse malicious archive")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error should mention block: %v", err)
	}
	// Skills dir must be untouched.
	entries, _ := os.ReadDir(skillsDir)
	if len(entries) != 0 {
		t.Errorf("skillsDir should be empty after blocked install: %+v", entries)
	}
}

func TestClient_Install_RefusesPathTraversalEntry(t *testing.T) {
	// Craft a tarball with a ../escape path.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "../escape.sh", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("evil"))
	tw.Close()
	gz.Close()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/skills/") {
			_, _ = w.Write([]byte(`{"slug":"x","version":"1","download_url":"` + srv.URL + `/dl"}`))
			return
		}
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", safety.NoopScanner{})
	_, err := c.Install(context.Background(), "x", "", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Errorf("expected traversal refusal, got: %v", err)
	}
}

type funcScanner struct {
	fn func(context.Context, string) (safety.Result, error)
}

func (f funcScanner) Name() string { return "func" }
func (f funcScanner) Scan(ctx context.Context, p string) (safety.Result, error) {
	return f.fn(ctx, p)
}
