package verify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeVerifier is a deterministic stand-in for time-bounded tests.
type fakeVerifier struct {
	name      string
	timeout   time.Duration
	ok        bool
	detail    string
	skipped   bool
	delay     time.Duration // simulate work for timeout tests
	callCount int
}

func (f *fakeVerifier) Name() string           { return f.name }
func (f *fakeVerifier) Timeout() time.Duration { return f.timeout }
func (f *fakeVerifier) Verify(ctx context.Context, path string, content []byte) (bool, string, bool) {
	f.callCount++
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return false, "timed out", true
		}
	}
	return f.ok, f.detail, f.skipped
}

func TestRegistry_LookupByExtension(t *testing.T) {
	reg := NewRegistry()
	goV := &fakeVerifier{name: "go"}
	tomlV := &fakeVerifier{name: "toml"}
	reg.Register(".go", goV)
	reg.Register(".toml", tomlV)

	if got := reg.Lookup("/tmp/foo.go"); got != goV {
		t.Errorf("want go verifier, got %v", got)
	}
	if got := reg.Lookup("/tmp/foo.toml"); got != tomlV {
		t.Errorf("want toml verifier, got %v", got)
	}
}

func TestRegistry_LookupIsCaseInsensitive(t *testing.T) {
	reg := NewRegistry()
	v := &fakeVerifier{name: "go"}
	reg.Register(".go", v)
	if got := reg.Lookup("/tmp/Foo.GO"); got != v {
		t.Errorf("case-insensitive lookup failed")
	}
}

func TestRegistry_FallbackEmptyKey(t *testing.T) {
	reg := NewRegistry()
	fb := &fakeVerifier{name: "fallback"}
	reg.Register("", fb)
	// Unknown extension should hit fallback.
	if got := reg.Lookup("/tmp/no-extension"); got != fb {
		t.Errorf("fallback not hit for unknown ext")
	}
}

func TestRegistry_NoMatchReturnsNil(t *testing.T) {
	reg := NewRegistry()
	if got := reg.Lookup("/tmp/foo.xyz"); got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestVerifyPaths_RunsVerifierPerPath(t *testing.T) {
	reg := NewRegistry()
	v := &fakeVerifier{name: "test", timeout: time.Second, ok: true}
	reg.Register(".go", v)

	paths := []string{"/tmp/a.go", "/tmp/b.go", "/tmp/c.go"}
	results := VerifyPaths(context.Background(), reg, paths)
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	if v.callCount != 3 {
		t.Errorf("verifier should run once per path, got %d calls", v.callCount)
	}
}

func TestVerifyPaths_MissingVerifierMarksSkipped(t *testing.T) {
	reg := NewRegistry()
	results := VerifyPaths(context.Background(), reg, []string{"/tmp/foo.unknown"})
	if len(results) != 1 {
		t.Fatal("want 1 result")
	}
	if !results[0].Skipped {
		t.Errorf("unknown ext should mark skipped: %+v", results[0])
	}
	if results[0].Verifier != "none" {
		t.Errorf("verifier label: %q", results[0].Verifier)
	}
}

func TestVerifyPaths_NilRegistryReturnsEmpty(t *testing.T) {
	if got := VerifyPaths(context.Background(), nil, []string{"foo"}); got != nil {
		t.Errorf("nil registry: %v", got)
	}
}

func TestVerifyPaths_TimeoutMarksSkipped(t *testing.T) {
	reg := NewRegistry()
	v := &fakeVerifier{
		name:    "slow",
		timeout: 50 * time.Millisecond,
		ok:      false,
		delay:   500 * time.Millisecond, // exceed timeout
		skipped: true,
		detail:  "timed out",
	}
	reg.Register(".go", v)
	results := VerifyPaths(context.Background(), reg, []string{"/tmp/slow.go"})
	if len(results) != 1 {
		t.Fatal("want 1 result")
	}
	if !results[0].Skipped {
		t.Errorf("timeout should be skipped, not fail: %+v", results[0])
	}
}

func TestFormatToolMessage_EmptyOnAllPassing(t *testing.T) {
	results := []VerifyResult{
		{Path: "a.go", Ok: true},
		{Path: "b.go", Ok: true},
	}
	if got := FormatToolMessage(results); got != "" {
		t.Errorf("all-pass should return empty, got %q", got)
	}
}

func TestFormatToolMessage_OmitsSkipped(t *testing.T) {
	// Skipped should NOT appear in the failures list — we don't
	// want to nag the model about files we couldn't check.
	results := []VerifyResult{
		{Path: "a.go", Ok: false, Skipped: true, Detail: "timed out"},
	}
	if got := FormatToolMessage(results); got != "" {
		t.Errorf("skipped should not surface as failure, got %q", got)
	}
}

func TestFormatToolMessage_RendersFailures(t *testing.T) {
	results := []VerifyResult{
		{Path: "a.go", Ok: false, Detail: "undefined: foo", Verifier: "go build"},
	}
	got := FormatToolMessage(results)
	if !strings.Contains(got, "a.go") {
		t.Errorf("file path missing: %q", got)
	}
	if !strings.Contains(got, "undefined: foo") {
		t.Errorf("error detail missing: %q", got)
	}
	if !strings.Contains(got, "1 problem") {
		t.Errorf("singular count: %q", got)
	}
}

func TestFormatToolMessage_PluralProblems(t *testing.T) {
	results := []VerifyResult{
		{Path: "a.go", Ok: false, Detail: "err1", Verifier: "go build"},
		{Path: "b.toml", Ok: false, Detail: "err2", Verifier: "toml parse"},
	}
	got := FormatToolMessage(results)
	if !strings.Contains(got, "2 problems") {
		t.Errorf("plural count: %q", got)
	}
}

// --- syntax verifier tests ---

func TestTOMLVerifier_ValidParses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.toml")
	if err := os.WriteFile(path, []byte("foo = \"bar\"\n[section]\nx = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := NewTOMLVerifier()
	ok, detail, skipped := v.Verify(context.Background(), path, nil)
	if !ok || skipped {
		t.Errorf("valid TOML: ok=%v skipped=%v detail=%q", ok, skipped, detail)
	}
}

func TestTOMLVerifier_InvalidFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	_ = os.WriteFile(path, []byte("this = is = not = toml"), 0o644)
	v := NewTOMLVerifier()
	ok, detail, _ := v.Verify(context.Background(), path, nil)
	if ok {
		t.Error("invalid TOML should not pass")
	}
	if detail == "" {
		t.Error("invalid TOML should include error detail")
	}
}

func TestJSONVerifier_ValidParses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.json")
	_ = os.WriteFile(path, []byte(`{"foo": "bar", "n": 42}`), 0o644)
	v := NewJSONVerifier()
	ok, _, _ := v.Verify(context.Background(), path, nil)
	if !ok {
		t.Error("valid JSON should pass")
	}
}

func TestJSONVerifier_TrailingCommaFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(path, []byte(`{"foo": "bar",}`), 0o644)
	v := NewJSONVerifier()
	ok, _, _ := v.Verify(context.Background(), path, nil)
	if ok {
		t.Error("trailing comma should fail")
	}
}

func TestYAMLVerifier_ValidParses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.yaml")
	_ = os.WriteFile(path, []byte("foo: bar\nlist:\n  - one\n  - two\n"), 0o644)
	v := NewYAMLVerifier()
	ok, _, _ := v.Verify(context.Background(), path, nil)
	if !ok {
		t.Error("valid YAML should pass")
	}
}

func TestYAMLVerifier_BadIndentFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(path, []byte("foo:\n  bar:\n   baz: 1\n     quux: 2"), 0o644)
	v := NewYAMLVerifier()
	ok, _, _ := v.Verify(context.Background(), path, nil)
	if ok {
		t.Error("inconsistent indent should fail")
	}
}

func TestSyntaxVerifier_MissingFileSkipped(t *testing.T) {
	v := NewJSONVerifier()
	ok, _, skipped := v.Verify(context.Background(), "/nonexistent/path.json", nil)
	if ok {
		t.Error("missing file should not pass")
	}
	if !skipped {
		t.Error("missing file should be skipped, not a hard failure")
	}
}

// --- path extraction tests ---

func TestExtractWritePaths_FromTopLevelKey(t *testing.T) {
	in := json.RawMessage(`{"path": "internal/foo.go", "content": "..."}`)
	got := ExtractWritePaths("fs_write", in)
	if len(got) != 1 || got[0] != "internal/foo.go" {
		t.Errorf("want [internal/foo.go], got %v", got)
	}
}

func TestExtractWritePaths_NestedFileArray(t *testing.T) {
	in := json.RawMessage(`{"files": [{"path": "a.go"}, {"path": "b.go"}]}`)
	got := ExtractWritePaths("patch", in)
	if len(got) != 2 {
		t.Fatalf("want 2, got %v", got)
	}
}

func TestExtractWritePaths_DeduplicatesPaths(t *testing.T) {
	in := json.RawMessage(`{"path": "same.go", "files": [{"path": "same.go"}]}`)
	got := ExtractWritePaths("fs_write", in)
	if len(got) != 1 {
		t.Errorf("dedup failed: %v", got)
	}
}

func TestExtractWritePaths_VariantKeys(t *testing.T) {
	cases := []string{
		`{"path": "a.go"}`,
		`{"file_path": "a.go"}`,
		`{"filepath": "a.go"}`,
		`{"target": "a.go"}`,
		`{"file": "a.go"}`,
	}
	for _, c := range cases {
		got := ExtractWritePaths("fs_write", json.RawMessage(c))
		if len(got) != 1 || got[0] != "a.go" {
			t.Errorf("key form %s: %v", c, got)
		}
	}
}

func TestExtractWritePaths_NonWriteToolReturnsNil(t *testing.T) {
	in := json.RawMessage(`{"path": "a.go"}`)
	if got := ExtractWritePaths("fs_read", in); got != nil {
		t.Errorf("non-write tool should return nil, got %v", got)
	}
}

func TestExtractWritePaths_MalformedJSONReturnsNil(t *testing.T) {
	if got := ExtractWritePaths("fs_write", json.RawMessage(`{not json`)); got != nil {
		t.Errorf("malformed JSON should return nil, got %v", got)
	}
}

func TestIsWriteTool_KnownAndUnknown(t *testing.T) {
	if !IsWriteTool("fs_write") {
		t.Error("fs_write should be classified as write")
	}
	if !IsWriteTool("FS_WRITE") {
		t.Error("case-insensitive")
	}
	if IsWriteTool("fs_read") {
		t.Error("fs_read should NOT be a write tool")
	}
	if IsWriteTool("") {
		t.Error("empty name")
	}
}

func TestDefaultRegistry_HasBuiltinVerifiers(t *testing.T) {
	r := DefaultRegistry()
	for _, ext := range []string{".go", ".toml", ".json", ".yaml", ".yml"} {
		if v := r.Lookup("file" + ext); v == nil {
			t.Errorf("default registry missing verifier for %s", ext)
		}
	}
}
