//go:build !windows

package automation

import (
	"os"
	"syscall"
)

func defaultPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 probes liveness without delivering anything (Unix).
	return proc.Signal(syscall.Signal(0)) == nil
}
