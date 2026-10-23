//go:build unix

package worktree

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockManagerFile(f *os.File) error   { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }
func unlockManagerFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
