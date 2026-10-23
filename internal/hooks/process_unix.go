//go:build unix

package hooks

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
)

func newHookCommand(_ context.Context, path string) (*exec.Cmd, error) {
	return exec.Command(path), nil
}
func runHookCommand(ctx context.Context, cmd *exec.Cmd) error { return processrunner.Run(ctx, cmd) }

// Unix hooks must be regular executable files owned by the current user.
// World-writable scripts are unsafe even when their owner matches.
func isExecutable(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 || info.Mode().Perm()&0022 != 0 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid())
}

func isTrustedDirectory(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid())
}
