package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellWorkingDirSymlinkContainment(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	shell := NewShellTool(func(s *ShellTool) { s.defaultWorkingDir = root })
	// A harmless command writes a sentinel only if validation wrongly admits it.
	raw, _ := json.Marshal(ShellInput{Command: "printf bad > sentinel", WorkingDir: filepath.Join(root, "escape")})
	if _, err := shell.Execute(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("escape admitted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "sentinel")); !os.IsNotExist(err) {
		t.Fatal("outside sentinel written")
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(child, filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(ShellInput{Command: "pwd", WorkingDir: "inside"})
	result, err := shell.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var output ShellOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(child)
	if strings.TrimSpace(output.Stdout) != resolved || !output.Completed {
		t.Fatalf("contained symlink: %#v", output)
	}
}
func TestShellStartupEnvironmentNotInheritedOrSupplied(t *testing.T) {
	for _, key := range []string{"ENV", "BASH_ENV", "SHELLOPTS", "BASHOPTS", "IFS", "CDPATH", "GLOBIGNORE", "PS4", "ZDOTDIR", "BASH_FUNC_payload%%"} {
		if !shellStartupEnv(key) {
			t.Errorf("startup key missed: %s", key)
		}
	}
	t.Setenv("ENV", "unsafe-startup-hook")
	t.Setenv("BASH_ENV", "unsafe-startup-hook")
	shell := NewShellTool()
	raw, _ := json.Marshal(ShellInput{Command: `printf '%s|%s|%s' "${ENV-unset}" "${BASH_ENV-unset}" "$OVERKILL_SAFE_TEST"`, Env: map[string]string{"ENV": "supplied-hook", "BASH_ENV": "supplied-hook", "OVERKILL_SAFE_TEST": "kept"}})
	result, err := shell.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	var output ShellOutput
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.Stdout) != "unset|unset|kept" || !output.Completed {
		t.Fatalf("environment: %#v", output)
	}
}
func TestShellMalformedCommandRejected(t *testing.T) {
	raw, _ := json.Marshal(ShellInput{Command: "printf '"})
	if _, err := NewShellTool().Execute(context.Background(), raw); err == nil {
		t.Fatal("malformed shell syntax admitted")
	}
}
