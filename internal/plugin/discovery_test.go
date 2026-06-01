package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscover_RejectsSymlinkBypass(t *testing.T) {
	// #55: A symlink in the plugins directory that points outside
	// the allowed path must not be followed or treated as a plugin.
	root := t.TempDir()

	// Create a legitimate plugin directory with plugin.toml.
	legit := filepath.Join(root, "legit")
	if err := os.MkdirAll(legit, 0o755); err != nil {
		t.Fatal(err)
	}
	legitTOML := `name = "legit"
version = "0.1.0"
entry = "./run.sh"
`
	if err := os.WriteFile(filepath.Join(legit, "plugin.toml"), []byte(legitTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside the plugins root that points to /tmp.
	// This should be rejected — it escapes the plugins sandbox.
	symlinkPath := filepath.Join(root, "escape")
	target := "/tmp"
	if err := os.Symlink(target, symlinkPath); err != nil {
		t.Fatal(err)
	}

	// Now try to discover plugins.
	discovered, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}

	// Check that no plugin was discovered from the symlink.
	for _, d := range discovered {
		if filepath.Base(d.EntryPath) == filepath.Base(symlinkPath) ||
			d.Name == "escape" {
			t.Errorf("symlink escape plugin should not be discovered: %+v", d)
		}
		if !filepath.HasPrefix(d.EntryPath, root) {
			t.Errorf("discovered plugin escapes root: %q not under %q", d.EntryPath, root)
		}
	}

	// The legitimate plugin must still be discovered.
	foundLegit := false
	for _, d := range discovered {
		if d.Name == "legit" {
			foundLegit = true
			break
		}
	}
	if !foundLegit {
		t.Error("legitimate plugin was not discovered")
	}
}

// #55: Symlink bypass in plugin path containment.
// The entry file in plugin.toml can be a symlink to outside
// the plugins root. The lexical HasPrefix check passes, but
// the OS follows the symlink at execution time.

func TestDiscover_EntrySymlinkEscapesSandbox(t *testing.T) {
	root := t.TempDir()

	// Create a legitimate-looking plugin directory inside root.
	pluginDir := filepath.Join(root, "my-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// plugin.toml points to "runner.sh" inside the dir.
	pluginTOML := "name = \"my-plugin\"\nversion = \"0.1.0\"\nentry = \"./runner.sh\"\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.toml"), []byte(pluginTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	// runner.sh is a symlink to /bin/sh — which is outside root.
	runnerPath := filepath.Join(pluginDir, "runner.sh")
	if err := os.Symlink("/bin/sh", runnerPath); err != nil {
		t.Fatal(err)
	}

	discovered, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}

	// The bug: the entry path passes lexical containment but
	// resolves to /bin/sh which is outside root.
	for _, d := range discovered {
		resolved, err := filepath.EvalSymlinks(d.EntryPath)
		if err != nil {
			continue
		}
		resolvedRoot, _ := filepath.EvalSymlinks(root)
		if !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) && resolved != resolvedRoot {
			t.Errorf("SYMLINK BYPASS #55: entry %q resolves to %q — outside root %q",
				d.EntryPath, resolved, resolvedRoot)
		}
	}
}
