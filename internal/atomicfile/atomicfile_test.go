package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	data := []byte("hello world")

	if err := WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("content mismatch: got %q, want %q", got, "hello world")
	}

	// No temp file should remain.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// Write initial content.
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	// Overwrite atomically.
	if err := WriteFile(path, []byte("new data"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != "new data" {
		t.Fatalf("content mismatch after overwrite: got %q", got)
	}
}

func TestWriteFile_Permissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perms.txt")

	if err := WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("perms: got %04o, want 0600", info.Mode().Perm())
	}
}

func TestWriteFile_CreatesParentDir(t *testing.T) {
	// WriteFile itself doesn't create dirs — it relies on os.CreateTemp
	// which needs the dir to exist. This is expected behavior.
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "deep", "test.txt")

	err := WriteFile(path, []byte("x"), 0644)
	if err == nil {
		t.Error("expected error when parent dirs don't exist")
	}
}

func TestWriteFile_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")

	if err := WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("WriteFile empty: %v", err)
	}

	got, _ := os.ReadFile(path)
	if len(got) != 0 {
		t.Fatalf("expected empty file, got %d bytes", len(got))
	}
}

func TestWriteFile_LargeData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")

	// 1MB of data.
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile large: %v", err)
	}

	got, _ := os.ReadFile(path)
	if len(got) != len(data) {
		t.Fatalf("size mismatch: got %d, want %d", len(got), len(data))
	}
	for i := range data {
		if got[i] != data[i] {
			t.Fatalf("byte %d mismatch: got %d, want %d", i, got[i], data[i])
		}
	}
}
