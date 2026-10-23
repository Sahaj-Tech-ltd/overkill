//go:build windows

package worktree

import (
	"golang.org/x/sys/windows"
	"os"
)

// Lock the whole file, including future appends. LockFileEx blocks until the
// preceding writer releases its lock; a process exit releases the kernel lock.
func lockManagerFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 0xffffffff, 0xffffffff, &windows.Overlapped{})
}
func unlockManagerFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 0xffffffff, 0xffffffff, &windows.Overlapped{})
}
