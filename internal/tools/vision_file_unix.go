//go:build !windows

package tools

import (
	"os"
	"syscall"
)

// Nonblocking open lets Stat reject pipes/devices without waiting for a writer.
// os.Root enforces workspace containment during traversal and symlink changes.
func openVisionFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
