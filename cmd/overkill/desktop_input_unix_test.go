//go:build unix

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDesktopInputCancelsInheritedBlockingSocket(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	reader := os.NewFile(uintptr(fds[0]), "inherited-parent-stdin")
	writer := os.NewFile(uintptr(fds[1]), "parent-writer")
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stop, err := ownDesktopInput(ctx, reader, cancel)
	require.NoError(t, err)
	cancel()
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		writer.Close()
		t.Fatal("parent reader did not interrupt its blocked socket read")
	}
}

func TestDesktopInputEOFStopsBackendLifetime(t *testing.T) {
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stop, err := ownDesktopInput(ctx, reader, cancel)
	require.NoError(t, err)
	defer stop()
	require.NoError(t, writer.Close())
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent EOF did not end backend lifetime")
	}
}
