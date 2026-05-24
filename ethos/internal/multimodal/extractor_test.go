package multimodal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeExt is a test stand-in for an Extractor.
type fakeExt struct {
	name   string
	mimes  []string
	exts   []string
	result Result
	err    error
	called bool
}

func (f *fakeExt) Name() string { return f.name }
func (f *fakeExt) Supports(mime, ext string) bool {
	for _, m := range f.mimes {
		if m == mime {
			return true
		}
	}
	for _, e := range f.exts {
		if e == ext {
			return true
		}
	}
	return false
}
func (f *fakeExt) Extract(ctx context.Context, path string) (Result, error) {
	f.called = true
	if f.err != nil {
		return Result{}, f.err
	}
	res := f.result
	res.Extractor = f.name
	return res, nil
}

func TestRegistry_LookupPicksFirstMatch(t *testing.T) {
	r := NewRegistry()
	first := &fakeExt{name: "first", mimes: []string{"text/plain"}}
	second := &fakeExt{name: "second", mimes: []string{"text/plain"}}
	r.Register(first)
	r.Register(second)

	got := r.Lookup("text/plain", ".txt")
	if got != first {
		t.Errorf("registry should pick FIRST match, got %v", got.Name())
	}
}

func TestRegistry_LookupMimeOrExt(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeExt{name: "by-mime", mimes: []string{"application/x-test"}})
	r.Register(&fakeExt{name: "by-ext", exts: []string{".specific"}})

	if got := r.Lookup("application/x-test", ".other"); got.Name() != "by-mime" {
		t.Errorf("mime match: %s", got.Name())
	}
	if got := r.Lookup("application/octet-stream", ".specific"); got.Name() != "by-ext" {
		t.Errorf("ext match: %s", got.Name())
	}
}

func TestRegistry_LookupNoMatchReturnsNil(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeExt{name: "narrow", mimes: []string{"x"}})
	if got := r.Lookup("y", ".z"); got != nil {
		t.Errorf("expected nil, got %s", got.Name())
	}
}

func TestRegistry_ExtractCallsMatched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	_ = os.WriteFile(path, []byte("hello world"), 0o644)

	want := &fakeExt{
		name:   "test",
		mimes:  []string{"text/plain; charset=utf-8"},
		exts:   []string{".txt"},
		result: Result{Text: "fake-result"},
	}
	r := NewRegistry()
	r.Register(want)

	res, err := r.Extract(context.Background(), path)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !want.called {
		t.Error("matched extractor was not called")
	}
	if res.Text != "fake-result" {
		t.Errorf("text: %q", res.Text)
	}
}

func TestDetect_KnownExtensions(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, body, wantExt string
	}{
		{"doc.txt", "hello", ".txt"},
		{"app.go", "package x", ".go"},
		{"data.json", `{"a":1}`, ".json"},
	}
	for _, c := range cases {
		path := filepath.Join(dir, c.name)
		_ = os.WriteFile(path, []byte(c.body), 0o644)
		mime, ext, err := Detect(path)
		if err != nil {
			t.Errorf("detect %s: %v", c.name, err)
			continue
		}
		if ext != c.wantExt {
			t.Errorf("%s ext: %q want %q", c.name, ext, c.wantExt)
		}
		if mime == "" {
			t.Errorf("%s mime empty", c.name)
		}
	}
}

func TestDetect_OOXMLCorrectionForDocx(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.docx")
	_ = os.WriteFile(path, []byte("PK\x03\x04rest of zip..."), 0o644)
	mime, _, _ := Detect(path)
	if !strings.Contains(mime, "wordprocessingml") {
		t.Errorf("docx OOXML correction failed, got mime=%q", mime)
	}
}

func TestDetect_OOXMLXLSX(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sheet.xlsx")
	_ = os.WriteFile(path, []byte("PK\x03\x04zip data..."), 0o644)
	mime, _, _ := Detect(path)
	if !strings.Contains(mime, "spreadsheetml") {
		t.Errorf("xlsx OOXML correction failed, got mime=%q", mime)
	}
}

func TestDetect_OOXMLPPTX(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deck.pptx")
	_ = os.WriteFile(path, []byte("PK\x03\x04zip data..."), 0o644)
	mime, _, _ := Detect(path)
	if !strings.Contains(mime, "presentationml") {
		t.Errorf("pptx OOXML correction failed, got mime=%q", mime)
	}
}

func TestDetect_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	_ = os.WriteFile(path, []byte{}, 0o644)
	// Empty file: Read returns (0, EOF). Detect treats 0-byte reads as error.
	_, ext, err := Detect(path)
	if err == nil {
		t.Fatal("expected error on empty file read")
	}
	if ext != ".txt" {
		t.Errorf("ext = %q, want .txt (should be set before read fails)", ext)
	}
}
func TestDetect_FileNotFound(t *testing.T) {
	_, _, err := Detect("/totally/does/not/exist.xyz")
	if err == nil {
		t.Error("missing file should error")
	}
}

func TestIsTextLike(t *testing.T) {
	tests := []struct {
		mime string
		want bool
	}{
		{"text/plain", true},
		{"text/html", true},
		{"text/css", true},
		{"text/csv", true},
		{"TEXT/PLAIN", true},
		{"application/json", true},
		{"application/xml", true},
		{"application/yaml", true},
		{"application/x-yaml", true},
		{"application/javascript", true},
		{"application/x-sh", true},
		{"application/pdf", false},
		{"image/png", false},
		{"application/octet-stream", false},
		{"video/mp4", false},
	}
	for _, tc := range tests {
		t.Run(tc.mime, func(t *testing.T) {
			if got := IsTextLike(tc.mime); got != tc.want {
				t.Errorf("IsTextLike(%q) = %v, want %v", tc.mime, got, tc.want)
			}
		})
	}
}

func TestTextExtractor_ReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	_ = os.WriteFile(path, []byte("hello\nworld\n"), 0o644)

	ex := NewTextExtractor()
	res, err := ex.Extract(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "hello") {
		t.Errorf("text missing content: %q", res.Text)
	}
	if res.Metadata["lines"] == "" {
		t.Error("metadata should include line count")
	}
}

func TestTextExtractor_CapsLargeFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.log")
	big := strings.Repeat("x", 500*1024)
	_ = os.WriteFile(path, []byte(big), 0o644)

	ex := NewTextExtractor()
	res, _ := ex.Extract(context.Background(), path)
	if len(res.Text) > 256*1024 {
		t.Errorf("text not capped: got %d bytes", len(res.Text))
	}
	if res.Metadata["truncated"] != "true" {
		t.Errorf("missing truncated=true in metadata: %v", res.Metadata)
	}
}

func TestTextExtractor_SupportsCodeExtensions(t *testing.T) {
	ex := NewTextExtractor()
	for _, e := range []string{".go", ".py", ".ts", ".toml", ".yaml", ".json", ".md"} {
		if !ex.Supports("application/octet-stream", e) {
			t.Errorf("should support %s", e)
		}
	}
}

func TestBinaryFallback_ClaimsAnything(t *testing.T) {
	b := NewBinaryFallback()
	if !b.Supports("anything/at/all", ".xyz") {
		t.Error("fallback must claim every file")
	}
}

func TestBinaryFallback_ProducesMetadataWhenContentUnknown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weird.bin")
	_ = os.WriteFile(path, []byte("\x00\x01\x02\x03\x04hello"), 0o644)

	b := NewBinaryFallback()
	res, err := b.Extract(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "binary file") {
		t.Errorf("text should explain it's a binary: %q", res.Text)
	}
	if res.Metadata["bytes"] == "" {
		t.Error("metadata should include byte count")
	}
}

func TestDefaultRegistry_HandlesAllCategories(t *testing.T) {
	r := DefaultRegistry(nil)
	cases := []struct {
		ext, wantExt string
	}{
		{".pdf", "pdftotext"},
		{".docx", "pandoc"},
		{".wav", "whisper"},
		{".png", "image"},
		{".go", "text"},
		{".xyz-not-real", "binary-fallback"},
	}
	for _, c := range cases {
		got := r.Lookup("application/octet-stream", c.ext)
		if got == nil {
			t.Errorf("%s: no extractor matched", c.ext)
			continue
		}
		if got.Name() != c.wantExt {
			t.Errorf("%s: matched %s, want %s", c.ext, got.Name(), c.wantExt)
		}
	}
}

func TestErrMissingDependency_FormatsInstallHint(t *testing.T) {
	err := &ErrMissingDependency{Tool: "pdftotext", InstallEx: "apt install poppler-utils"}
	if !strings.Contains(err.Error(), "pdftotext") {
		t.Error("error missing tool name")
	}
	if !strings.Contains(err.Error(), "apt install") {
		t.Error("error missing install hint")
	}
}

func TestErrMissingDependency_NoInstallHint(t *testing.T) {
	err := &ErrMissingDependency{Tool: "whatever"}
	if !strings.Contains(err.Error(), "whatever") {
		t.Error("error missing tool name")
	}
	if strings.Contains(err.Error(), "install:") {
		t.Error("should not say 'install:' when no hint provided")
	}
}

// ---------------------------------------------------------------------------
// New edge-case tests
// ---------------------------------------------------------------------------

func TestRegistry_EmptyLookupReturnsNil(t *testing.T) {
	r := NewRegistry()
	if got := r.Lookup("text/plain", ".txt"); got != nil {
		t.Errorf("empty registry should return nil, got %v", got)
	}
}

func TestRegistry_EmptyExtractReturnsErrNoExtractor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("hi"), 0o644)

	r := NewRegistry()
	_, err := r.Extract(context.Background(), path)
	if err != ErrNoExtractor {
		t.Errorf("expected ErrNoExtractor, got %v", err)
	}
}

func TestRegistry_LookupNormalizesMimeAndExt(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeExt{name: "t", mimes: []string{"text/plain"}, exts: []string{".md"}})

	if got := r.Lookup(" TEXT/PLAIN ", " .MD "); got == nil {
		t.Error("lookup with whitespace+case should still match")
	}
}
