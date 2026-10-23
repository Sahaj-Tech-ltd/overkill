//go:build unix

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
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
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
	if err := os.MkdirAll(point, 0o750); err != nil {
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
	if err := os.MkdirAll(point, 0o750); err != nil {
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
	if err := os.MkdirAll(point, 0o750); err != nil {
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
	_ = os.WriteFile(non, []byte("x"), 0o600)
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

func TestUserHookTrustRejectsWritableAndLinkedScripts(t *testing.T) {
	root := t.TempDir()
	point := filepath.Join(root, string(BeforeToolCall))
	if err := os.Mkdir(point, 0700); err != nil {
		t.Fatal(err)
	}
	script := writeScript(t, point, "#!/bin/sh\nexit 0\n", true)
	if err := os.Chmod(script, 0770); err != nil {
		t.Fatal(err)
	}
	if isExecutable(script) {
		t.Fatal("group-writable hook must not execute")
	}
	if err := os.Chmod(script, 0700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(point, "linked.sh")
	if err := os.Symlink(script, linked); err != nil {
		t.Fatal(err)
	}
	if isExecutable(linked) {
		t.Fatal("linked hook must not execute")
	}
	if err := os.Chmod(point, 0770); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistryFromDir(root); err == nil {
		t.Fatal("group-writable hook directory must reject runtime assembly")
	}
	if err := os.Chmod(point, 0700); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(t.TempDir(), "hooks")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistryFromDir(rootLink); err == nil {
		t.Fatal("linked hooks root must not be trusted")
	}
}

func TestLoadedHookRechecksTrustAndReceivesDispatchPoint(t *testing.T) {
	root := t.TempDir()
	point := filepath.Join(root, string(AfterTurn))
	if err := os.Mkdir(point, 0700); err != nil {
		t.Fatal(err)
	}
	captured := filepath.Join(point, "captured.json")
	script := writeScript(t, point, "#!/bin/sh\ncat > \"$(dirname \"$0\")/captured.json\"\n", true)
	reg, err := NewRegistryFromDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Fire(context.Background(), AfterTurn, Event{SessionID: "owned-session"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(captured)
	if err != nil || !contains(string(data), `"point":"after_turn"`) {
		t.Fatalf("dispatch point missing: %s, %v", data, err)
	}
	if err := os.Remove(captured); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(script, 0770); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Fire(context.Background(), AfterTurn, Event{}); err == nil {
		t.Fatal("changed script trust must be checked again at execution")
	}
	if _, err := os.Stat(captured); !os.IsNotExist(err) {
		t.Fatalf("unsafe script executed: %v", err)
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
