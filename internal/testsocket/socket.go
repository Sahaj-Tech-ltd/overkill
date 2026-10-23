// Package testsocket creates private, short-lived paths for Unix socket tests.
package testsocket

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Path avoids test-name prefixes and the long default macOS TMPDIR. Unix
// sockaddr limits apply to pathname bytes, including the socket filename.
func Path(t testing.TB) string {
	t.Helper()
	base := "/tmp"
	if runtime.GOOS == "windows" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "ok-sock-")
	if err != nil {
		t.Fatalf("create short socket directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove socket directory: %v", err)
		}
	})
	return filepath.Join(dir, "rpc.sock")
}
