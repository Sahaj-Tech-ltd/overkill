//go:build unix

package journal

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockJournalFile(f *os.File) error   { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }
func unlockJournalFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }

func openJournalFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
}
