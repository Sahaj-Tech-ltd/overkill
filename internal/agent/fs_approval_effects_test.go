package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestUnifiedFilesystemWriteHonorsConfiguredApproval(t *testing.T) {
	root := t.TempDir()
	registry := tools.NewRegistry()
	require.NoError(t, registry.Register(tools.NewFSTool(root)))
	provider := &mockProvider{responses: []providers.Response{
		{ToolCalls: []providers.ToolCall{{ID: "write", Name: "fs", Arguments: `{"action":"write","path":"proof.txt","content":"changed"}`}}},
		{Content: "The write was declined."},
	}}
	a := newTestAgent(provider, registry, nil, nil)
	defer a.Shutdown()
	approvals := 0
	a.SetApprovalFunc(func(name, args, risk string) Approval {
		approvals++
		require.Equal(t, "fs", name)
		require.Equal(t, "medium", risk)
		return Approval{Allow: false}
	})
	_, err := a.Run(t.Context(), "Attempt a bounded file edit")
	require.NoError(t, err)
	if approvals != 1 {
		t.Errorf("configured approval callback count = %d; want 1", approvals)
	}
	_, err = os.Stat(filepath.Join(root, "proof.txt"))
	require.True(t, os.IsNotExist(err), "declined write changed workspace")
}
