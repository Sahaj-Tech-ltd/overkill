package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTakeSnapshot(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main"), 0644)

	snap, err := TakeSnapshot(context.Background(), dir)
	if err != nil {
		t.Fatalf("TakeSnapshot: %v", err)
	}

	if snap.Time.IsZero() {
		t.Error("snapshot time is zero")
	}
	if len(snap.Files) != 0 && snap.GitSHA != "non-git" {
		// If git repo, should have SHA
	}
}

func TestCheckFileClaim_Missing(t *testing.T) {
	a := &Auditor{WorkDir: t.TempDir()}
	r := &Report{Passed: true}

	a.checkFileClaim(context.Background(),
		Claim{Description: "add config.go", Files: []string{"config.go"}},
		"config.go", nil, r)

	if r.Passed {
		t.Error("should have failed for missing file")
	}
}

func TestCheckFileClaim_Exists(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.go"), []byte("package main"), 0644)

	a := &Auditor{WorkDir: dir}
	r := &Report{Passed: true}

	a.checkFileClaim(context.Background(),
		Claim{Description: "add config.go", Files: []string{"config.go"}},
		"config.go", nil, r)

	if !r.Passed {
		t.Error("should have passed for existing file")
	}
}

func TestCheckFileClaim_Unchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := []byte("package main\nfunc main() {}")
	os.WriteFile(path, content, 0600)

	preFiles := map[string]string{"main.go": fileHash(path)}

	a := &Auditor{WorkDir: dir}
	r := &Report{Passed: true}

	a.checkFileClaim(context.Background(),
		Claim{Description: "modify main.go", Files: []string{"main.go"}},
		"main.go", &preFiles, r)

	if r.Passed {
		t.Error("should have failed for unchanged file when pre-snapshot expected change")
	}
}

func TestCheckFileClaim_ActuallyChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	os.WriteFile(path, []byte("package main\nfunc main() {}"), 0600)

	preFiles := map[string]string{"main.go": "oldhash123"}

	a := &Auditor{WorkDir: dir}
	r := &Report{Passed: true}

	a.checkFileClaim(context.Background(),
		Claim{Description: "modify main.go", Files: []string{"main.go"}},
		"main.go", &preFiles, r)

	if !r.Passed {
		t.Error("should have passed — file exists and hash is different from oldhash")
	}
}

func TestFirstLines(t *testing.T) {
	s := "line1\nline2\nline3\nline4\nline5\nline6"
	if got := firstLines(s, 3); !strings.Contains(got, "line1") || strings.Contains(got, "line4") {
		t.Errorf("firstLines(3) = %q", got)
	}
}

func TestFileHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello"), 0600)

	h1 := fileHash(path)
	h2 := fileHash(path)
	if h1 != h2 {
		t.Error("hash should be deterministic")
	}
	if h1 == "" {
		t.Error("hash should not be empty")
	}
}

func TestFileHash_DifferentContent(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	os.WriteFile(a, []byte("hello"), 0600)
	os.WriteFile(b, []byte("world"), 0600)

	if fileHash(a) == fileHash(b) {
		t.Error("different content should produce different hashes")
	}
}
