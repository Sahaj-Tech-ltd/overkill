//go:build unix

package term

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	xterm "golang.org/x/term"
)

func openProbeTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close(); slave.Close() })
	return master, slave
}

func TestProbeTimeoutReleasesTerminalInput(t *testing.T) {
	t.Setenv("OVERKILL_RUNNING", "")
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "no reply", true: "partial reply"}[partial], func(t *testing.T) {
			master, slave := openProbeTerminal(t)
			before, err := xterm.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			flags, err := unix.FcntlInt(slave.Fd(), unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if partial {
				// Queue an incomplete response before the probe; raw mode exposes it.
				if _, err := master.Write([]byte("\x1b]11;rgb:0000/")); err != nil {
					t.Fatal(err)
				}
			}
			start := time.Now()
			_, err = queryBackground(slave, &output, 40*time.Millisecond)
			if err == nil || err.Error() != "term: probe timeout" {
				t.Fatalf("expected timeout, got %v", err)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("unbounded probe: %v", elapsed)
			}
			if output.String() != "\x1b]11;?\x07" {
				t.Fatalf("wrong query: %q", output.String())
			}
			after, err := xterm.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("probe did not restore terminal mode")
			}
			afterFlags, err := unix.FcntlInt(slave.Fd(), unix.F_GETFL, 0)
			if err != nil {
				t.Fatal(err)
			}
			if flags != afterFlags {
				t.Fatalf("descriptor flags changed: %d -> %d", flags, afterFlags)
			}
			// Simulate the frontend claiming stdin AFTER timeout. No background
			// reader may consume these bytes, including a late terminal response.
			old, err := xterm.MakeRaw(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			defer xterm.Restore(int(slave.Fd()), old)
			const later = "frontend input\x07"
			if _, err := master.Write([]byte(later)); err != nil {
				t.Fatal(err)
			}
			got, err := readProbeReply(slave, time.Now().Add(time.Second))
			if err != nil || string(got) != later {
				t.Fatalf("stdin stolen after timeout: %q, %v", got, err)
			}
		})
	}
}

func TestProbeReplyDoesNotPrefetchFrontendKeys(t *testing.T) {
	t.Setenv("OVERKILL_RUNNING", "")
	for _, ending := range []string{"\x07", "\x1b\\"} {
		t.Run(ending, func(t *testing.T) {
			master, slave := openProbeTerminal(t)
			const tail = "next command\x07"
			if _, err := master.Write([]byte("\x1b]11;rgb:0000/0000/0000" + ending + tail)); err != nil {
				t.Fatal(err)
			}
			dark, err := queryBackground(slave, &bytes.Buffer{}, time.Second)
			if err != nil || !dark {
				t.Fatalf("reply classification: dark=%t err=%v", dark, err)
			}
			old, err := xterm.MakeRaw(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			defer xterm.Restore(int(slave.Fd()), old)
			got, err := readProbeReply(slave, time.Now().Add(time.Second))
			if err != nil || string(got) != tail {
				t.Fatalf("prefetched frontend keys: %q %v", got, err)
			}
		})
	}
}

func TestProbeReadBoundAndHangup(t *testing.T) {
	master, slave := openProbeTerminal(t)
	old, err := xterm.MakeRaw(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer xterm.Restore(int(slave.Fd()), old)
	if _, err := master.Write(bytes.Repeat([]byte("x"), 65)); err != nil {
		t.Fatal(err)
	}
	got, err := readProbeReply(slave, time.Now().Add(time.Second))
	if len(got) != 64 || err == nil || err.Error() != "term: probe response too long" {
		t.Fatalf("bound: %d bytes, %v", len(got), err)
	}
	master.Close()
	_, err = readProbeReply(slave, time.Now().Add(time.Second))
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("hangup not reported: %v", err)
	}
}
