//go:build windows

package journal

import (
	"golang.org/x/sys/windows"
	"os"
)

// Lock the whole file, including future appends. LockFileEx blocks until the
// preceding writer releases its lock; a process exit releases the kernel lock.
func lockJournalFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 0xffffffff, 0xffffffff, &windows.Overlapped{})
}
func unlockJournalFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 0xffffffff, 0xffffffff, &windows.Overlapped{})
}

// LockFileEx needs GENERIC_READ or GENERIC_WRITE. Go's Windows append-only
// handle drops GENERIC_WRITE, so retain read access alongside append access.
func openJournalFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
}
