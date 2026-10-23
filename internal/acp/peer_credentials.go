package acp

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ReadPeerCredential reads a trusted local private reference. All errors remain
// generic: credential bytes/path contents never enter model-visible errors.
func ReadPeerCredential(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute private peer credential reference required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return "", fmt.Errorf("private regular peer credential file required")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("peer credential unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Size() > 4096 || (runtime.GOOS != "windows" && opened.Mode().Perm()&0077 != 0) {
		return "", fmt.Errorf("peer credential identity changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return "", fmt.Errorf("invalid peer credential")
	}
	token := strings.TrimSpace(string(data))
	raw, err := hex.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return "", fmt.Errorf("invalid scoped peer credential")
	}
	return token, nil
}
