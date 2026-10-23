//go:build unix

package main

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
)

const cliTerminalAskSupported = true

// The console is the sole reader. Canonical terminals become readable at a
// complete line; polling before each byte also supports cancellation on pipes
// without a reader goroutine surviving a cancelled turn.
func readCLIByte(ctx context.Context, input *os.File, b []byte) (int, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		fds := []unix.PollFd{{Fd: int32(input.Fd()), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 {
			continue
		}
		if fds[0].Revents&unix.POLLNVAL != 0 {
			return 0, os.ErrClosed
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err = unix.Read(int(input.Fd()), b)
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		if n == 0 && err == nil {
			return 0, io.EOF
		}
		return n, err
	}
}
