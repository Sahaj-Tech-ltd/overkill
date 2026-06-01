package hooks

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeScript(t *testing.T, dir, body string, executable bool) string {
	t.Helper()
	path := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if executable {
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestLoadFromDir_RegistersExecutableScripts(t *testing.T) {
	root := t.TempDir()
	point := filepath.Join(root, string(BeforeToolCall))
	if err := os.MkdirAll(point, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, point, "#!/bin/sh\nexit 0\n", true)

	reg := NewRegistry()
	count, err := LoadFromDir(reg, root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if count != 1 {
		t.Fatalf("count=%d want 1", count)
	}
	hooks := reg.List(BeforeToolCall)
	if len(hooks) != 1 {
		t.Fatalf("registry has %d hooks at point", len(hooks))
	}
}

func TestLoadFromDir_SkipsNonExecutable(t *testing.T) {
	root := t.TempDir()
	point := filepath.Join(root, string(AfterToolCall))
	if err := os.MkdirAll(point, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, point, "#!/bin/sh\nexit 0\n", false)

	reg := NewRegistry()
	count, err := LoadFromDir(reg, root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if count != 0 {
		t.Fatalf("non-executable should be skipped, count=%d", count)
	}
}

func TestLoadFromDir_MissingDirOK(t *testing.T) {
	reg := NewRegistry()
	count, err := LoadFromDir(reg, "/no/such/path/abcxyz")
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if count != 0 {
		t.Fatalf("count should be 0, got %d", count)
	}
}

func TestLoadedHook_PipesEventToStdin(t *testing.T) {
	root := t.TempDir()
	point := filepath.Join(root, string(OnError))
	if err := os.MkdirAll(point, 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "captured.txt")
	body := "#!/bin/sh\ncat > " + out + "\n"
	writeScript(t, point, body, true)

	reg := NewRegistry()
	if _, err := LoadFromDir(reg, root); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := reg.Fire(context.Background(), OnError, Event{Point: OnError, SessionID: "s-test"}); err != nil {
		t.Fatalf("fire: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read captured: %v", err)
	}
	if !contains(string(got), "s-test") {
		t.Fatalf("script did not see event: %q", got)
	}
}

func TestIsExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := writeScript(t, dir, "#!/bin/sh\n", true)
	non := filepath.Join(dir, "not_exe")
	_ = os.WriteFile(non, []byte("x"), 0o644)
	if !isExecutable(exe) {
		t.Error("expected executable")
	}
	if isExecutable(non) {
		t.Error("expected non-executable")
	}
	if isExecutable(filepath.Join(dir, "missing")) {
		t.Error("missing file should not be executable")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
