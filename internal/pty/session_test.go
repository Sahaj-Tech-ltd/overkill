package pty

import (
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSessionEcho(t *testing.T) {
	s := New()
	cmd := exec.Command("bash", "-c", "echo hi")
	if err := s.Start(cmd); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()

	// Drain output until the child exits or read errors out.
	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		b := make([]byte, 1024)
		for {
			n, err := s.Read(b)
			if n > 0 {
				buf.Write(b[:n])
			}
			if err != nil {
				done <- buf.String()
				return
			}
		}
	}()

	select {
	case out := <-done:
		if !strings.Contains(out, "hi") {
			t.Fatalf("expected 'hi' in output, got %q", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for output")
	}

	code, err := s.WaitExit()
	if err != nil {
		t.Fatalf("waitexit: %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
}

func TestSessionWriteAndResize(t *testing.T) {
	s := New()
	cmd := exec.Command("cat")
	if err := s.Start(cmd); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()

	if err := s.Resize(24, 80); err != nil {
		t.Fatalf("resize: %v", err)
	}

	if _, err := s.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 256)
		var sb strings.Builder
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			n, err := s.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
				if strings.Contains(sb.String(), "hello") {
					got <- sb.String()
					return
				}
			}
			if err != nil && err != io.EOF {
				return
			}
		}
		got <- sb.String()
	}()

	select {
	case out := <-got:
		if !strings.Contains(out, "hello") {
			t.Fatalf("expected echoed 'hello', got %q", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("read timeout")
	}
}
