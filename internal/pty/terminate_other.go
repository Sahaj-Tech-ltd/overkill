//go:build !unix

package pty

import "os"

func terminateOwned(process *os.Process) error { return process.Kill() }
