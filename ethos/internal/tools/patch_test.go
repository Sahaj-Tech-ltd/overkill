package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUnifiedDiff_Basic(t *testing.T) {
	patch := `@@ -1,3 +1,4 @@
 a
 b
+x
 c
`
	hunks, err := ParseUnifiedDiff(patch)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	h := hunks[0]
	if h.OldStart != 1 || h.OldCount != 3 || h.NewStart != 1 || h.NewCount != 4 {
		t.Fatalf("unexpected hunk header: %+v", h)
	}
	if len(h.Lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(h.Lines))
	}
}

func TestParseUnifiedDiff_Malformed(t *testing.T) {
	cases := []struct {
		name  string
		patch string
	}{
		{"no hunks", "no header here\n"},
		{"bad header", "@@ broken @@\n"},
		{"weird tag", "@@ -1,1 +1,1 @@\n!bogus\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseUnifiedDiff(tc.patch); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

func TestApplyHunks_SimpleAdd(t *testing.T) {
	src := "a\nb\nc\n"
	patch := `@@ -1,3 +1,4 @@
 a
 b
+x
 c
`
	hunks, _ := ParseUnifiedDiff(patch)
	out, err := ApplyHunks(src, hunks)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out != "a\nb\nx\nc\n" {
		t.Fatalf("got %q", out)
	}
}

func TestApplyHunks_SimpleRemove(t *testing.T) {
	src := "a\nb\nc\n"
	patch := `@@ -1,3 +1,2 @@
 a
-b
 c
`
	hunks, _ := ParseUnifiedDiff(patch)
	out, err := ApplyHunks(src, hunks)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out != "a\nc\n" {
		t.Fatalf("got %q", out)
	}
}

func TestApplyHunks_Conflict(t *testing.T) {
	src := "a\nWRONG\nc\n"
	patch := `@@ -1,3 +1,3 @@
 a
-b
+B
 c
`
	hunks, _ := ParseUnifiedDiff(patch)
	if _, err := ApplyHunks(src, hunks); err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestApplyHunks_PreserveNoTrailingNewline(t *testing.T) {
	src := "a\nb"
	patch := `@@ -1,2 +1,2 @@
 a
-b
+B
`
	hunks, _ := ParseUnifiedDiff(patch)
	out, err := ApplyHunks(src, hunks)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if out != "a\nB" {
		t.Fatalf("got %q", out)
	}
}

func TestPatchTool_Execute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewPatchTool(dir)
	in := PatchInput{
		Path: "f.txt",
		Patch: `@@ -1,3 +1,4 @@
 a
 b
+x
 c
`,
	}
	raw, _ := json.Marshal(in)
	out, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var got PatchOutput
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.HunksApplied != 1 {
		t.Fatalf("got HunksApplied %d", got.HunksApplied)
	}
	final, _ := os.ReadFile(path)
	if string(final) != "a\nb\nx\nc\n" {
		t.Fatalf("file got %q", string(final))
	}
}

func TestPatchTool_MissingArgs(t *testing.T) {
	tool := NewPatchTool("/tmp")
	if _, err := tool.Execute(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("expected error on empty input")
	}
	if _, err := tool.Execute(context.Background(), []byte(`{"path":"x"}`)); err == nil {
		t.Fatal("expected error on missing patch")
	}
}

func TestApplyHunks_MultipleHunks(t *testing.T) {
	src := "a\nb\nc\nd\ne\nf\n"
	patch := `@@ -1,2 +1,3 @@
 a
+X
 b
@@ -5,2 +6,2 @@
 e
-f
+F
`
	hunks, err := ParseUnifiedDiff(patch)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hunks) != 2 {
		t.Fatalf("expected 2 hunks, got %d", len(hunks))
	}
	out, err := ApplyHunks(src, hunks)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "a\nX\nb\nc\nd\ne\nF\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestParseUnifiedDiff_SkipsFileHeaders(t *testing.T) {
	patch := `--- a/x
+++ b/x
@@ -1,1 +1,1 @@
-a
+b
`
	hunks, err := ParseUnifiedDiff(patch)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	if !strings.Contains(hunks[0].Lines[0], "a") {
		t.Fatalf("lost first line: %v", hunks[0].Lines)
	}
}
