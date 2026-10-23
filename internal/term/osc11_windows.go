//go:build windows

package term

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	probeKernel32   = windows.NewLazySystemDLL("kernel32.dll")
	probePeekInput  = probeKernel32.NewProc("PeekConsoleInputW")
	probeReadInput  = probeKernel32.NewProc("ReadConsoleInputW")
	probeWriteInput = probeKernel32.NewProc("WriteConsoleInputW")
)

// INPUT_RECORD contains a WORD event type, padding, and a 16-byte union on
// both Windows architectures. ReadConsoleInputW is used instead of ReadFile:
// a console handle can be signalled by mouse, resize, and key-up events even
// when a byte-oriented read would still block.
type probeInputRecord [20]byte

// readProbeReply owns console input only during the synchronous startup probe.
// Peek and read are safe here because QueryBackground must run before any UI
// input reader starts. There is no goroutine left reading after the deadline.
func readProbeReply(input *os.File, deadline time.Time) (reply []byte, err error) {
	handle := windows.Handle(input.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil, fmt.Errorf("term: console mode: %w", err)
	}
	var preserved []probeInputRecord
	defer func() {
		// Keep unrelated events available to the application that starts next.
		// Appending can move them behind events arriving during the probe, but
		// never leaves an asynchronous consumer or discards those events.
		for len(preserved) > 0 {
			var written uint32
			ok, _, callErr := probeWriteInput.Call(uintptr(handle), uintptr(unsafe.Pointer(&preserved[0])), uintptr(len(preserved)), uintptr(unsafe.Pointer(&written)))
			if ok == 0 || written == 0 {
				err = errors.Join(err, fmt.Errorf("term: restore console input: %w", callErr))
				return
			}
			preserved = preserved[written:]
		}
	}()
	for len(reply) < 64 {
		if !time.Now().Before(deadline) {
			return reply, errors.New("term: probe timeout")
		}
		var record probeInputRecord
		var count uint32
		ok, _, callErr := probePeekInput.Call(uintptr(handle), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			return reply, fmt.Errorf("term: peek console input: %w", callErr)
		}
		if count == 0 {
			time.Sleep(min(2*time.Millisecond, time.Until(deadline)))
			continue
		}
		ok, _, callErr = probeReadInput.Call(uintptr(handle), uintptr(unsafe.Pointer(&record)), 1, uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			return reply, fmt.Errorf("term: read console input: %w", callErr)
		}
		if count == 0 {
			continue
		}
		character, repeats, isReply := probeRecordCharacter(record)
		if !isReply {
			preserved = append(preserved, record)
			// Bound memory even if a mouse or other event source floods input.
			if len(preserved) >= 256 {
				return reply, errors.New("term: too many unrelated console events")
			}
			continue
		}
		for repeats > 0 {
			reply = append(reply, character)
			repeats--
			if character == '\x07' || character == '\\' || len(reply) == 64 {
				if repeats > 0 {
					binary.LittleEndian.PutUint16(record[8:10], repeats)
					preserved = append(preserved, record)
				}
				if character == '\x07' || character == '\\' {
					return reply, nil
				}
				return reply, errors.New("term: probe response too long")
			}
		}
	}
	return reply, errors.New("term: probe response too long")
}

func probeRecordCharacter(record probeInputRecord) (byte, uint16, bool) {
	if binary.LittleEndian.Uint16(record[:2]) != 1 || binary.LittleEndian.Uint32(record[4:8]) == 0 {
		return 0, 0, false // Not a key-down event.
	}
	character := binary.LittleEndian.Uint16(record[14:16])
	repeats := binary.LittleEndian.Uint16(record[8:10])
	if character == 0 || character > 127 || repeats == 0 {
		return 0, 0, false // OSC replies are ASCII, not virtual keys or UTF-16.
	}
	return byte(character), repeats, true
}
