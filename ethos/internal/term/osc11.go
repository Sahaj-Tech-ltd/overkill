// Package term — OSC 10/11 background-color probe (master plan §5.1).
//
// Some terminals (kitty, iTerm2, modern xterm, gnome-terminal) reply to an
// OSC 11 query with their current background color. We probe once at boot
// and use the brightness to decide dark vs light mode, then call back to
// the caller so they can flip the theme.
//
// Designed to never block startup: the probe runs with a tight deadline
// and silently degrades when the terminal doesn't reply.
package term

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/term"
)

// QueryBackground returns true when the terminal's background is dark, false
// when it's light, or an error when the probe fails (no terminal, no reply,
// timeout). Best-effort — callers should treat error as "stick with default".
func QueryBackground(timeout time.Duration) (dark bool, err error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return false, errors.New("term: stdin not a tty")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return false, fmt.Errorf("term: makeraw: %w", err)
	}
	defer term.Restore(fd, old)

	if _, err := io.WriteString(os.Stdout, "\x1b]11;?\x07"); err != nil {
		return false, fmt.Errorf("term: write probe: %w", err)
	}

	if timeout <= 0 {
		timeout = 200 * time.Millisecond
	}
	type result struct {
		raw []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		r := bufio.NewReader(os.Stdin)
		buf := make([]byte, 0, 64)
		for i := 0; i < 64; i++ {
			b, err := r.ReadByte()
			if err != nil {
				ch <- result{buf, err}
				return
			}
			buf = append(buf, b)
			if b == '\x07' || b == '\\' {
				ch <- result{buf, nil}
				return
			}
		}
		ch <- result{buf, errors.New("term: probe response too long")}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return false, r.err
		}
		return parseBackgroundReply(r.raw)
	case <-time.After(timeout):
		return false, errors.New("term: probe timeout")
	}
}

// rgb captures the per-channel hex from `\x1b]11;rgb:RRRR/GGGG/BBBB`.
var rgbRe = regexp.MustCompile(`rgb:([0-9a-fA-F]+)/([0-9a-fA-F]+)/([0-9a-fA-F]+)`)

// parseBackgroundReply extracts the RGB triple and returns true when its
// luminance is below the dark/light midpoint (≈ 0.5 in linear space).
func parseBackgroundReply(reply []byte) (bool, error) {
	m := rgbRe.FindStringSubmatch(string(reply))
	if len(m) != 4 {
		return false, fmt.Errorf("term: unrecognized OSC 11 reply: %q", reply)
	}
	r, err := parseColor(m[1])
	if err != nil {
		return false, err
	}
	g, err := parseColor(m[2])
	if err != nil {
		return false, err
	}
	b, err := parseColor(m[3])
	if err != nil {
		return false, err
	}
	// Rec. 709 luma; threshold 0.5 splits dark vs light.
	luma := 0.2126*r + 0.7152*g + 0.0722*b
	return luma < 0.5, nil
}

// parseColor normalizes a 16-bit hex channel into [0,1].
func parseColor(hex string) (float64, error) {
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("term: bad color %q: %w", hex, err)
	}
	scale := float64(uint64(1)<<(len(hex)*4) - 1)
	if scale == 0 {
		scale = 1
	}
	return float64(v) / scale, nil
}
