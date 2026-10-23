package verify

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilesystemWriteActionsOnly(t *testing.T) {
	for _, action := range []string{"write", "edit", "read", "stat", "glob", "mkdir", "unknown"} {
		input, err := json.Marshal(map[string]string{"action": action, "path": "file.json"})
		require.NoError(t, err)
		want := action == "write" || action == "edit"
		require.Equal(t, want, IsWriteCall("fs", input), action)
		require.Equal(t, want, len(ExtractWritePaths("fs", input)) == 1, action)
	}
	require.False(t, IsWriteCall("fs", json.RawMessage(`{invalid`)))
}
