//go:build windows

package coordination

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func repositoryDirectoryIdentity(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("coordination: repository identity is not a directory")
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	var identity windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &identity); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", identity.VolumeSerialNumber, identity.FileIndexHigh, identity.FileIndexLow), nil
}
