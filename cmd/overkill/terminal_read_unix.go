//go:build unix

package main

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func readEscapeTail(input *os.File, buffer []byte) (n int, readErr error) {
	fd := int(input.Fd())
	flags, err := unix.FcntlInt(input.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return 0, err
	}
	if err = unix.SetNonblock(fd, true); err != nil {
		return 0, err
	}
	defer func() { _, err := unix.FcntlInt(input.Fd(), unix.F_SETFL, flags); readErr = errors.Join(readErr, err) }()
	n, err = unix.Read(fd, buffer)
	if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
		return 0, nil
	}
	return n, err
}
