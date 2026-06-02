//go:build windows

package checks

import "golang.org/x/sys/windows"

func diskFree(path string) (uint64, uint64, error) {
	var freeBytesAvailable, totalBytes uint64
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeBytesAvailable, &totalBytes, nil); err != nil {
		return 0, 0, err
	}
	return freeBytesAvailable, totalBytes, nil
}
