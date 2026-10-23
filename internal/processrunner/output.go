// Package processrunner executes bounded subprocess trees on Unix and Windows.
package processrunner

import (
	"context"
	"fmt"
	"os/exec"
)

func CombinedOutput(ctx context.Context, cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil || cmd.Stderr != nil {
		return nil, fmt.Errorf("processrunner: output already configured")
	}
	output := NewOutputCapture(1024 * 1024)
	cmd.Stdout = output
	cmd.Stderr = output
	err := Run(ctx, cmd)
	text, _, _ := output.Snapshot()
	return []byte(text), err
}
