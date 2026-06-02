package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeQuerier is a controllable LSPQuerier for tests.
type fakeQuerier struct {
	available bool
	hover     HoverResult
	defs      []LocationResult
	refs      []LocationResult
	err       error

	lastCtx  context.Context
	lastFile string
	lastLine int
	lastCol  int
}

func (f *fakeQuerier) Available(path string) bool { return f.available }
func (f *fakeQuerier) Hover(ctx context.Context, p string, l, c int) (HoverResult, error) {
	f.lastCtx, f.lastFile, f.lastLine, f.lastCol = ctx, p, l, c
	return f.hover, f.err
}
func (f *fakeQuerier) Definition(ctx context.Context, p string, l, c int) ([]LocationResult, error) {
	f.lastCtx, f.lastFile, f.lastLine, f.lastCol = ctx, p, l, c
	return f.defs, f.err
}
func (f *fakeQuerier) References(ctx context.Context, p string, l, c int) ([]LocationResult, error) {
	f.lastCtx, f.lastFile, f.lastLine, f.lastCol = ctx, p, l, c
	return f.refs, f.err
}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func TestLSPHover_ReturnsContents(t *testing.T) {
	q := &fakeQuerier{available: true, hover: HoverResult{Contents: "func Foo()"}}
	tool := NewLSPHoverTool(q)
	in, _ := json.Marshal(lspPositionInput{File: "main.go", Line: 1, Column: 2})

	out, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	got := decode(t, out)
	if got["contents"] != "func Foo()" {
		t.Errorf("contents = %v; want %q", got["contents"], "func Foo()")
	}
	if q.lastFile != "main.go" || q.lastLine != 1 || q.lastCol != 2 {
		t.Errorf("forwarded args = %s:%d:%d", q.lastFile, q.lastLine, q.lastCol)
	}
	if q.lastCtx == nil {
		t.Errorf("ctx not forwarded")
	}
}

func TestLSPHover_NilQuerier(t *testing.T) {
	tool := NewLSPHoverTool(nil)
	out, err := tool.Execute(context.Background(), []byte(`{"file":"x.go"}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := decode(t, out); !strings.Contains(got["error"].(string), "lsp not available") {
		t.Errorf("expected lsp not available, got %v", got)
	}
}

func TestLSPHover_MissingFile(t *testing.T) {
	q := &fakeQuerier{available: true}
	tool := NewLSPHoverTool(q)
	out, err := tool.Execute(context.Background(), []byte(`{"line":1}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := decode(t, out); got["error"] == nil {
		t.Errorf("expected error for missing file, got %v", got)
	}
}

func TestLSPHover_RejectsNewline(t *testing.T) {
	q := &fakeQuerier{available: true}
	tool := NewLSPHoverTool(q)
	in, _ := json.Marshal(lspPositionInput{File: "bad\nfile.go"})
	out, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := decode(t, out); got["error"] == nil || !strings.Contains(got["error"].(string), "newline") {
		t.Errorf("expected newline rejection, got %v", got)
	}
}

func TestLSPHover_NoServer(t *testing.T) {
	q := &fakeQuerier{available: false}
	tool := NewLSPHoverTool(q)
	in, _ := json.Marshal(lspPositionInput{File: "main.rb"})
	out, _ := tool.Execute(context.Background(), in)
	if got := decode(t, out); !strings.Contains(got["error"].(string), "no language server") {
		t.Errorf("expected no language server error, got %v", got)
	}
}

func TestLSPHover_SurfacesErrorAsJSON(t *testing.T) {
	q := &fakeQuerier{available: true, err: errors.New("rpc timeout")}
	tool := NewLSPHoverTool(q)
	in, _ := json.Marshal(lspPositionInput{File: "main.go"})
	out, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute should not return error: %v", err)
	}
	if got := decode(t, out); got["error"] != "rpc timeout" {
		t.Errorf("expected rpc timeout, got %v", got)
	}
}

func TestLSPDefinition_ReturnsLocations(t *testing.T) {
	q := &fakeQuerier{
		available: true,
		defs:      []LocationResult{{File: "/a.go", Line: 10, Column: 4}},
	}
	tool := NewLSPDefinitionTool(q)
	in, _ := json.Marshal(lspPositionInput{File: "/b.go", Line: 1, Column: 1})
	out, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got := decode(t, out)
	if got["count"].(float64) != 1 {
		t.Errorf("count = %v; want 1", got["count"])
	}
	locs, ok := got["locations"].([]any)
	if !ok || len(locs) != 1 {
		t.Fatalf("locations shape wrong: %v", got)
	}
}

func TestLSPDefinition_MalformedJSON(t *testing.T) {
	tool := NewLSPDefinitionTool(&fakeQuerier{available: true})
	_, err := tool.Execute(context.Background(), []byte(`{not json`))
	if err == nil {
		t.Errorf("expected unmarshal error")
	}
}

func TestLSPReferences_ReturnsLocations(t *testing.T) {
	q := &fakeQuerier{
		available: true,
		refs: []LocationResult{
			{File: "/a.go", Line: 1},
			{File: "/b.go", Line: 2},
		},
	}
	tool := NewLSPReferencesTool(q)
	in, _ := json.Marshal(lspPositionInput{File: "/x.go", Line: 3, Column: 4})
	out, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got := decode(t, out)
	if got["count"].(float64) != 2 {
		t.Errorf("count = %v; want 2", got["count"])
	}
}

func TestLSPReferences_NilQuerier(t *testing.T) {
	tool := NewLSPReferencesTool(nil)
	out, _ := tool.Execute(context.Background(), []byte(`{"file":"x.go"}`))
	if got := decode(t, out); !strings.Contains(got["error"].(string), "lsp not available") {
		t.Errorf("expected lsp not available, got %v", got)
	}
}

func TestValidatePath(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"empty", "", true},
		{"newline", "a\nb", true},
		{"carriage return", "a\rb", true},
		{"control char", "\x01file", true},
		{"relative", "foo/bar.go", false},
		{"absolute", "/abs/path.go", false},
		{"tab in middle ok", "foo\tbar.go", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePath(tc.path)
			if (err != nil) != tc.wantErr {
				t.Errorf("validatePath(%q) err=%v wantErr=%v", tc.path, err, tc.wantErr)
			}
		})
	}
}

func TestLSPToolNames(t *testing.T) {
	q := &fakeQuerier{}
	checks := map[string]string{
		"lsp_hover":      NewLSPHoverTool(q).Name(),
		"lsp_definition": NewLSPDefinitionTool(q).Name(),
		"lsp_references": NewLSPReferencesTool(q).Name(),
	}
	for want, got := range checks {
		if got != want {
			t.Errorf("name = %q; want %q", got, want)
		}
	}
}
