//go:build windows

package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

// Console input is a waitable handle on Windows. In VT input mode, queued
// arrow-key bytes can be read once it is signaled; an unaccompanied Escape
// times out without ever switching a console handle to socket-only nonblocking.
func readEscapeTail(input *os.File, buffer []byte) (int, error) {
	handle := windows.Handle(input.Fd())
	status, err := windows.WaitForSingleObject(handle, 20)
	if err != nil {
		return 0, err
	}
	if status == uint32(windows.WAIT_TIMEOUT) {
		return 0, nil
	}
	if status != windows.WAIT_OBJECT_0 {
		return 0, fmt.Errorf("unexpected terminal wait result: %d", status)
	}
	var n uint32
	err = windows.ReadFile(handle, buffer, &n, nil)
	return int(n), err
}
