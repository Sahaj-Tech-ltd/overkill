package safety

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNoopScanner_AlwaysClean(t *testing.T) {
	r, err := NoopScanner{}.Scan(context.Background(), "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != VerdictClean {
		t.Errorf("noop should always return clean, got %s", r.Verdict)
	}
}

func TestNewVirusTotalScanner_EmptyKeyReturnsNil(t *testing.T) {
	if NewVirusTotalScanner("") != nil {
		t.Error("empty API key should yield nil scanner")
	}
	if NewVirusTotalScanner("   ") != nil {
		t.Error("whitespace API key should yield nil scanner")
	}
	if NewVirusTotalScanner("real-key") == nil {
		t.Error("real key should yield a scanner")
	}
}

func TestVirusTotalScanner_404IsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "clean.txt")
	writeFile(t, path, "innocent content")

	sc := NewVirusTotalScanner("k").WithBaseURL(srv.URL)
	r, err := sc.Scan(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != VerdictUnknown {
		t.Errorf("404 should be Unknown, got %s", r.Verdict)
	}
	if r.SHA256 == "" {
		t.Error("SHA256 should be populated even on Unknown")
	}
}

func TestVirusTotalScanner_RateLimitIsUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	writeFile(t, path, "x")

	sc := NewVirusTotalScanner("k").WithBaseURL(srv.URL)
	r, _ := sc.Scan(context.Background(), path)
	if r.Verdict != VerdictUnknown {
		t.Errorf("429 should be Unknown, got %s", r.Verdict)
	}
	if !strings.Contains(r.Reason, "rate-limited") {
		t.Errorf("reason should mention rate-limit: %q", r.Reason)
	}
}

func TestVirusTotalScanner_MaliciousDetection(t *testing.T) {
	body := `{
		"data": {
			"attributes": {
				"last_analysis_stats": {"malicious": 5, "suspicious": 2},
				"last_analysis_results": {
					"EngineA": {"category": "malicious", "result": "Trojan.Foo"},
					"EngineB": {"category": "malicious", "result": "Malware.Bar"}
				}
			}
		}
	}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.sh")
	writeFile(t, path, "malicious payload here")

	sc := NewVirusTotalScanner("k").WithBaseURL(srv.URL)
	r, err := sc.Scan(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != VerdictMalicious {
		t.Errorf("expected Malicious, got %s", r.Verdict)
	}
	if r.Detections["EngineA"] != "Trojan.Foo" {
		t.Errorf("detection label not propagated: %+v", r.Detections)
	}
}

func TestVirusTotalScanner_SuspiciousVerdict(t *testing.T) {
	body := `{"data":{"attributes":{
		"last_analysis_stats":{"malicious":1,"suspicious":0},
		"last_analysis_results":{}
	}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	writeFile(t, path, "x")

	sc := NewVirusTotalScanner("k").WithBaseURL(srv.URL)
	r, _ := sc.Scan(context.Background(), path)
	if r.Verdict != VerdictSuspicious {
		t.Errorf("1 malicious below threshold of 2 → Suspicious, got %s", r.Verdict)
	}
}

func TestVirusTotalScanner_CleanWhenNoDetections(t *testing.T) {
	body := `{"data":{"attributes":{
		"last_analysis_stats":{"malicious":0,"suspicious":0},
		"last_analysis_results":{}
	}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	writeFile(t, path, "x")

	sc := NewVirusTotalScanner("k").WithBaseURL(srv.URL)
	r, _ := sc.Scan(context.Background(), path)
	if r.Verdict != VerdictClean {
		t.Errorf("zero detections → Clean, got %s", r.Verdict)
	}
}

func TestVirusTotalScanner_PassesAPIKeyHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-apikey")
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	writeFile(t, path, "x")

	sc := NewVirusTotalScanner("the-key").WithBaseURL(srv.URL)
	_, _ = sc.Scan(context.Background(), path)
	if got != "the-key" {
		t.Errorf("API key header not set, got %q", got)
	}
}

func TestScanDir_PicksWorstVerdict(t *testing.T) {
	// Stub scanner: returns Malicious on any file named "bad",
	// Clean otherwise.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ok.txt"), "innocent")
	writeFile(t, filepath.Join(dir, "bad.sh"), "evil")
	writeFile(t, filepath.Join(dir, "sub", "another.txt"), "fine")

	stub := stubVerdictScanner{
		verdictByName: map[string]Verdict{"bad.sh": VerdictMalicious},
	}
	results, worst, err := ScanDir(context.Background(), stub, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	if worst != VerdictMalicious {
		t.Errorf("worst should be Malicious, got %s", worst)
	}
}

func TestScanDir_NilScannerUsesNoop(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a"), "x")
	results, worst, err := ScanDir(context.Background(), nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if worst != VerdictClean {
		t.Errorf("nil scanner → noop → clean, got %s", worst)
	}
	if len(results) != 1 || results[0].Verdict != VerdictClean {
		t.Errorf("unexpected results: %+v", results)
	}
}

type stubVerdictScanner struct {
	verdictByName map[string]Verdict
}

func (s stubVerdictScanner) Name() string { return "stub" }
func (s stubVerdictScanner) Scan(_ context.Context, p string) (Result, error) {
	v, ok := s.verdictByName[filepath.Base(p)]
	if !ok {
		v = VerdictClean
	}
	return Result{Path: p, Verdict: v}, nil
}
