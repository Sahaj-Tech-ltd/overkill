//go:build !windows

package tools

import (
	"encoding/json"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVisionRejectsFIFOWithoutWaitingForWriter(t *testing.T) {
	workspace := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(workspace, "pipe.png"), 0600))
	d := &boundaryVisionDescriber{}
	tool := NewVisionDescribeTool(d, nil, BrowserHostPolicy{}).WithRootDir(workspace)
	out, err := tool.Execute(t.Context(), json.RawMessage(`{"file":"pipe.png"}`))
	require.NoError(t, err)
	require.Contains(t, string(out), "regular file")
	require.Zero(t, d.calls)
}
