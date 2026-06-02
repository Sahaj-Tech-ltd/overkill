//go:build !windows

package main

import (
	"os"
	"syscall"
)

func pidIsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 probes liveness without delivering anything.
	return proc.Signal(syscall.Signal(0)) == nil
}
