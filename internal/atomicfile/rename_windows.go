//go:build windows

package atomicfile

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"time"
)

// Windows readers and virus scanners may briefly hold a destination without
// delete sharing. Retry replacement without removing the prior complete file.
func replaceFile(from, to string) error {
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Rename(from, to)
		if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) && !errors.Is(err, windows.ERROR_LOCK_VIOLATION)) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
