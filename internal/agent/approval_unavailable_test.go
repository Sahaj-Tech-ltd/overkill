package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApprovalRequiredWithoutClientFailsClosed(t *testing.T) {
	for _, level := range []string{"safe", "yolo", "auto"} {
		t.Run(level, func(t *testing.T) {
			a := &Agent{}
			a.SetAutoMode(level)
			require.Equal(t, level != "safe", a.checkToolApproval("fs", `{"action":"read","path":"source.go"}`))
			require.Equal(t, level != "safe", a.checkToolApproval("fs", `{"action":"write","path":"source.go","content":"changed"}`))
			require.Equal(t, level == "auto", a.checkToolApproval("shell", `{"command":"echo hello"}`))
		})
	}
}
