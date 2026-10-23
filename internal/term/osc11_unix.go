//go:build unix

package term

import (
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// The caller owns terminal input until this returns. Nonblocking reads prevent
// a readiness race from extending the deadline; flags are restored before the
// frontend takes over. One-byte reads never prefetch later keyboard input.
func readProbeReply(input *os.File, deadline time.Time) (reply []byte, readErr error) {
	fd := int(input.Fd())
	flags, err := unix.FcntlInt(input.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return nil, err
	}
	if err = unix.SetNonblock(fd, true); err != nil {
		return nil, err
	}
	defer func() {
		_, err := unix.FcntlInt(input.Fd(), unix.F_SETFL, flags)
		readErr = errors.Join(readErr, err)
	}()
	for len(reply) < 64 {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return reply, errors.New("term: probe timeout")
		}
		millis := remaining / time.Millisecond
		if remaining%time.Millisecond != 0 {
			millis++
		}
		if millis > 1<<31-1 {
			millis = 1<<31 - 1
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(millis))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return reply, err
		}
		if n == 0 {
			continue
		}
		if fds[0].Revents&unix.POLLNVAL != 0 {
			return reply, os.ErrClosed
		}
		if !time.Now().Before(deadline) {
			return reply, errors.New("term: probe timeout")
		}
		var b [1]byte
		n, err = unix.Read(fd, b[:])
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			continue
		}
		if err != nil {
			return reply, err
		}
		if n == 0 {
			return reply, io.EOF
		}
		reply = append(reply, b[0])
		if b[0] == '\x07' || b[0] == '\\' {
			return reply, nil
		}
	}
	return reply, errors.New("term: probe response too long")
}
