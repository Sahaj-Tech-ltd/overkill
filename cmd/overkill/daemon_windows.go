//go:build windows

package main

import "os"

func pidIsRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Windows, Signal only works for the current process.
	// os.FindProcess always succeeds — just assume it's running.
	_ = proc
	return true
}
