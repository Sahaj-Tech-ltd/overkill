//go:build unix

package pty

import (
	"errors"
	"os"
	"syscall"
)

// creack/pty starts a new session with this process as its group leader.
// Closing an owned terminal retires the whole group, including foreground
// commands and background descendants still in that group.
func terminateOwned(process *os.Process) error {
	err := syscall.Kill(-process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
