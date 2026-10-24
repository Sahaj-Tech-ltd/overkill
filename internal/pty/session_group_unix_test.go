//go:build unix

package pty

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCloseRetiresBackgroundDescendants(t *testing.T) {
	target := filepath.Join(t.TempDir(), "late-effect")
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	cmd := exec.Command("sh", "-c", `(sleep 0.3; printf leaked > "$1") & echo ready; wait`, "fixture", target)
	if err := s.Start(cmd); err != nil {
		t.Fatal(err)
	}
	ready := make(chan bool, 1)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := s.Read(buf)
			if strings.Contains(string(buf[:n]), "ready") {
				ready <- true
				return
			}
			if err != nil {
				ready <- false
				return
			}
		}
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("no readiness")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("startup timeout")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _, _ = s.WaitExit(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("owned process did not stop")
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("background process survived terminal close: %v", err)
	}
}
