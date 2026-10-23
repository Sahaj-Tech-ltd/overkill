//go:build unix

package main

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Node's child stdin is an inherited blocking Unix socket. Closing an ordinary
// os.Stdin File does not interrupt a blocking read on macOS. Register an owned
// nonblocking duplicate with Go's poller before starting the lifetime reader.
func cancellableDesktopInput(input io.ReadCloser) (io.ReadCloser, error) {
	file, ok := input.(*os.File)
	if !ok {
		return input, nil
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 {
		return nil, fmt.Errorf("desktop parent must provide a pipe or socket")
	}
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, err
	}
	owned := os.NewFile(uintptr(fd), "overkill-desktop-parent")
	if owned == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("cannot own desktop parent pipe")
	}
	if err := file.Close(); err != nil {
		owned.Close()
		return nil, err
	}
	return owned, nil
}
