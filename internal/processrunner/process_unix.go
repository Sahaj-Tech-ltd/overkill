//go:build unix

package processrunner

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// Run owns a process group, bounds pipe draining and terminates descendants
// when the invocation ends. Callers configure command arguments/env/IO normally;
// process attributes are owned here so children cannot evade cancellation by
// inheriting the application's process group.
func Run(ctx context.Context, cmd *exec.Cmd) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	terminate := func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return errors.Join(err, ctx.Err(), terminate())
	case <-ctx.Done():
		killErr := terminate()
		// WaitDelay bounds inherited stdout/stderr pipes after the process exits.
		waitErr := <-done
		return errors.Join(ctx.Err(), killErr, waitErr)
	}
}
