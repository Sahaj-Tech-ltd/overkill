//go:build unix

package checkpoint

import "syscall"

// A regular file swapped for a FIFO between lstat/open must not hang admission.
const checkpointOpenFlags = syscall.O_NONBLOCK
