//go:build windows

package automation

func defaultPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// On Windows, os.FindProcess always succeeds (doesn't check existence)
	// and Process.Signal only supports os.Kill/os.Interrupt.
	// Assume the PID is alive — the GracePeriod timeout handles stale PIDs.
	return true
}
