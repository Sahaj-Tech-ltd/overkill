//go:build unix

package testsocket

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathBindsWhenDefaultTemporaryDirectoryIsLong(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("long-temp-", 10))
	if err := os.MkdirAll(long, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", long)
	path := Path(t)
	if len(path) > 103 {
		t.Fatalf("socket path has %d bytes", len(path))
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("bind short path under long TMPDIR: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}
