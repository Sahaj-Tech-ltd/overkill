// Package pty wraps github.com/creack/pty to provide a simple Session type
// for running interactive commands inside a pseudo-terminal.
package pty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// Session is a single PTY-backed command. Construct with New, then Start.
type Session struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	pty    *os.File
	exit   int
	done   chan struct{}
	closed bool
	close  sync.Once
}

// New returns a session ready to be started.
func New() *Session {
	return &Session{done: make(chan struct{})}
}

// Start launches cmd inside a freshly allocated PTY. The returned session
// owns the PTY file descriptor. The caller is responsible for calling
// Close() when finished.
func (s *Session) Start(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("pty: session already closed")
	}
	if s.pty != nil {
		return errors.New("pty: session already started")
	}
	f, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("pty: start: %w", err)
	}
	s.cmd = cmd
	s.pty = f
	s.done = make(chan struct{})
	go func() {
		if err := cmd.Wait(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				s.exit = exit.ExitCode()
			} else {
				s.exit = -1
			}
		}
		close(s.done)
	}()
	return nil
}

// Read pulls bytes from the PTY's master side.
func (s *Session) Read(buf []byte) (int, error) {
	s.mu.Lock()
	f := s.pty
	s.mu.Unlock()
	if f == nil {
		return 0, io.EOF
	}
	return f.Read(buf)
}

// Write pushes bytes into the PTY's master side (visible to the child as stdin).
func (s *Session) Write(buf []byte) (int, error) {
	s.mu.Lock()
	f := s.pty
	s.mu.Unlock()
	if f == nil {
		return 0, errors.New("pty: not started")
	}
	return f.Write(buf)
}

// Resize tells the kernel the new window size for the slave terminal.
func (s *Session) Resize(rows, cols uint16) error {
	s.mu.Lock()
	f := s.pty
	s.mu.Unlock()
	if f == nil {
		return errors.New("pty: not started")
	}
	return pty.Setsize(f, &pty.Winsize{Rows: rows, Cols: cols})
}

// WaitExit blocks until the child has terminated and returns its exit code.
func (s *Session) WaitExit() (int, error) {
	<-s.done
	return s.exit, nil
}

// Close kills the child if still running and releases the PTY.
func (s *Session) Close() error {
	var err error
	s.close.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.closed = true
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		if s.pty != nil {
			err = s.pty.Close()
			s.pty = nil
		}
	})
	return err
}
