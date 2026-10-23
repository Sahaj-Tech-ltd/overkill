package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestConfiguredReadonlyCannotBeOverriddenByWriterGate(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	require.NoError(t, registry.Register(tools.NewFSTool(root)))
	require.NoError(t, registry.Register(tools.NewShellTool()))
	a := &Agent{toolRegistry: registry}
	a.SetAutoMode("readonly")
	// The CLI installs its ordinary writer gate after applying configuration.
	a.SetPrivilegeGate(security.NewPrivilegeGate(security.ModeWriter))
	a.SetPrivilegeMode(security.ModeWriter)
	_, err := a.executeTool(t.Context(), "fs", json.RawMessage(`{"action":"write","path":"proof.txt","content":"changed"}`))
	_, statErr := os.Stat(filepath.Join(root, "proof.txt"))
	t.Logf("configured readonly actual fs write: error=%v file-created=%v", err, statErr == nil)
	require.ErrorIs(t, err, security.ErrWriteDenied)
	require.True(t, os.IsNotExist(statErr))
	require.Equal(t, security.ModeReader, a.PrivilegeMode())
	require.NoError(t, os.WriteFile(filepath.Join(root, "read.txt"), []byte("original"), 0600))
	output, err := a.executeTool(t.Context(), "fs", json.RawMessage(`{"action":"read","path":"read.txt"}`))
	require.NoError(t, err)
	require.Contains(t, string(output), "original")
	raw, err := json.Marshal(tools.ShellInput{Command: "printf changed > shell-proof.txt", WorkingDir: root})
	require.NoError(t, err)
	_, err = a.executeTool(t.Context(), "shell", raw)
	require.ErrorIs(t, err, security.ErrWriteDenied)
	_, err = os.Stat(filepath.Join(root, "shell-proof.txt"))
	require.True(t, os.IsNotExist(err))
	// A subsequent trusted configuration change deliberately restores writes.
	a.SetAutoMode("supervised")
	_, err = a.executeTool(t.Context(), "fs", json.RawMessage(`{"action":"write","path":"writer.txt","content":"written"}`))
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, "writer.txt"))
	require.NoError(t, err)
	require.Equal(t, "written", string(data))
}
