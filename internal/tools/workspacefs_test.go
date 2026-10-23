package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runFS(t *testing.T, root string, in FSInput) (ToolResult, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	out, err := NewFSTool(root).Execute(context.Background(), raw)
	var result ToolResult
	if err == nil {
		err = json.Unmarshal(out, &result)
	}
	return result, err
}

func TestWorkspaceSymlinkRootAndContainment(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	outside := filepath.Join(dir, "outside")
	for _, p := range []string{workspace, outside} {
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(workspace, "secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := runFS(t, alias, FSInput{Action: "write", Path: "nested/ok", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	for _, in := range []FSInput{
		{Action: "read", Path: "nested/ok"}, {Action: "stat", Path: "nested/ok"},
		{Action: "read", Path: filepath.Join(workspace, "nested/ok")},
		{Action: "write", Path: filepath.Join(workspace, "canonical-new"), Content: "canonical"}, {Action: "glob", Pattern: "nested/*"},
		{Action: "edit", Path: "nested/ok", Old: "hello", New: "world"}, {Action: "mkdir", Path: "more/dirs"},
	} {
		result, err := runFS(t, alias, in)
		if err != nil || !result.Success {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	for _, in := range []FSInput{
		{Action: "read", Path: "escape/secret"}, {Action: "read", Path: "secret"},
		{Action: "write", Path: "escape/new", Content: "bad"}, {Action: "write", Path: "secret", Content: "bad"},
		{Action: "write", Path: "escape/sub/new", Content: "bad"}, {Action: "mkdir", Path: "escape/newdir"},
		{Action: "edit", Path: "escape/secret", Old: "old", New: "bad"}, {Action: "stat", Path: "secret"},
		{Action: "write", Path: "../outside/new", Content: "bad"},
	} {
		if _, err := runFS(t, alias, in); err == nil {
			t.Fatalf("escape accepted: %+v", in)
		}
	}
	result, err := runFS(t, alias, FSInput{Action: "glob", Pattern: "escape/*"})
	if err != nil || result.Output != "[]" {
		t.Fatalf("escaped glob: %+v %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(outside, "secret"))
	if err != nil || string(data) != "old\n" {
		t.Fatalf("outside changed: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatalf("created outside: %v", err)
	}
}

func TestWorkspacePatchContainmentAndMode(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	original := []byte("old\n")
	if err := os.WriteFile(filepath.Join(outside, "file"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct{ name, target string }{{"parent", outside}, {"direct", filepath.Join(outside, "file")}} {
		if err := os.Symlink(link.target, filepath.Join(dir, link.name)); err != nil {
			t.Fatal(err)
		}
	}
	patch := "@@ -1 +1 @@\n-old\n+new\n"
	for _, path := range []string{"parent/file", "direct"} {
		raw, _ := json.Marshal(PatchInput{Path: path, Patch: patch})
		if _, err := NewPatchTool(dir).Execute(context.Background(), raw); err == nil {
			t.Fatalf("patch escape %s", path)
		}
	}
	path := filepath.Join(dir, "local")
	if err := os.WriteFile(path, original, 0o751); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(PatchInput{Path: "local", Patch: patch})
	if _, err := NewPatchTool(dir).Execute(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o751 {
		t.Fatalf("mode lost: %v %v", info, err)
	}
	for _, in := range []FSInput{{Action: "write", Path: "local", Content: "next"}, {Action: "edit", Path: "local", Old: "next", New: "last"}} {
		if _, err := runFS(t, dir, in); err != nil {
			t.Fatal(err)
		}
		info, err = os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o751 {
			t.Fatal("mode lost")
		}
	}
	data, _ := os.ReadFile(filepath.Join(outside, "file"))
	if string(data) != string(original) {
		t.Fatal("outside changed")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".overkill-") {
			t.Fatal("temporary file leaked")
		}
	}
}

func TestWorkspaceParentReplacedAfterOpen(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	root, name, err := openWorkspace(dir, "parent/new")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Remove(filepath.Join(dir, "parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "parent")); err != nil {
		t.Fatal(err)
	}
	if err := atomicWorkspaceWrite(root, name, []byte("bad")); err == nil {
		t.Fatal("write followed swapped parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatal("outside file created")
	}
}

func TestReadBoundedHugeLineAndPinnedDescriptor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge")
	fh, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	chunk := strings.Repeat("x", 32*1024)
	for i := 0; i < 1024; i++ {
		if _, err := io.WriteString(fh, chunk); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.WriteString(fh, "\nlast\n"); err != nil {
		t.Fatal(err)
	}
	fh.Close()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	result, err := runFS(t, dir, FSInput{Action: "read", Path: "huge", Offset: 2, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if result.Output != "2: last\n" {
		t.Fatalf("range mismatch: %q", result.Output)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2*1024*1024 {
		t.Fatalf("ranged read allocated %d bytes", allocated)
	}
	pinned, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&before)
	raw, err := largeFileReference(context.Background(), pinned, path, 32*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2*1024*1024 {
		t.Fatalf("peek allocated %d bytes", allocated)
	}
	if strings.Contains(string(raw), "replacement") || !strings.Contains(string(raw), "line truncated") {
		t.Fatalf("wrong descriptor or no truncation: %.200s", raw)
	}
	if len(raw) > 20000 {
		t.Fatalf("huge line produced %d bytes", len(raw))
	}
}

func TestStreamReadRangeStopsEarly(t *testing.T) {
	// A reader that refuses a second read proves small ranges do not consume EOF.
	var out []string
	err := streamLines(context.Background(), &oneReadReader{}, func(n int, line string) bool { out = append(out, fmt.Sprintf("%d:%s", n, line)); return n < 2 })
	if err != nil || strings.Join(out, ",") != "1:first,2:second" {
		t.Fatalf("%v %v", out, err)
	}
}

type oneReadReader struct{ read bool }

func (r *oneReadReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, fmt.Errorf("read beyond range")
	}
	r.read = true
	return copy(p, "first\nsecond\n"), nil
}

func TestWorkspaceInternalRelativeLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := runFS(t, dir, FSInput{Action: "write", Path: "linked/new", Content: "original"}); err != nil {
		t.Fatal(err)
	}
	result, err := runFS(t, dir, FSInput{Action: "read", Path: "linked/new"})
	if err != nil || result.Output != "1: original\n" {
		t.Fatalf("relative link failed: %+v %v", result, err)
	}
	if err := os.Symlink("real/new", filepath.Join(dir, "file-link")); err != nil {
		t.Fatal(err)
	}
	result, err = runFS(t, dir, FSInput{Action: "read", Path: "file-link"})
	if err != nil || !strings.Contains(result.Output, "original") {
		t.Fatalf("file link failed: %+v %v", result, err)
	}
	if _, err := runFS(t, dir, FSInput{Action: "write", Path: "file-link", Content: "changed"}); err == nil {
		t.Fatal("mutation must explicitly reject final symlinks")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "real/new"))
	if string(data) != "original" {
		t.Fatal("symlink target changed")
	}
}
