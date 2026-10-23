package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

func TestProductionAutomaticCheckpointRunAndStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "stream"}[stream], func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			workspace, err := filepath.EvalSymlinks(workspace)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "file.txt"), []byte("before"), 0o755))
			services, err := newRuntimeServices(config.Default(), nil, home)
			require.NoError(t, err)
			metadata := session.NewSession(workspace)
			runtime, err := services.BuildSession(t.Context(), metadata)
			require.NoError(t, err)
			defer runtime.Cleanup()
			require.NotNil(t, runtime.CheckpointSnapshotter)
			a := agent.New(agent.Config{Provider: &verificationScript{path: "file.txt", broken: "after"}, Tools: runtime.Tools, SessionID: metadata.ID, Model: "fixture", MaxSteps: 4})
			defer a.Shutdown()
			a.SetCheckpointSnapshotter(runtime.CheckpointSnapshotter)
			if stream {
				events, err := a.Stream(t.Context(), "write file")
				require.NoError(t, err)
				for event := range events {
					require.NoError(t, event.Error)
				}
			} else {
				_, err = a.Run(t.Context(), "write file")
				require.NoError(t, err)
			}
			raw, err := os.ReadFile(filepath.Join(workspace, "file.txt"))
			require.NoError(t, err)
			require.Equal(t, "after", string(raw))
			manager, err := checkpoint.NewSessionManager(filepath.Join(home, "checkpoints"), 5, workspace, metadata.ID)
			require.NoError(t, err)
			checkpoints, err := manager.List(metadata.ID)
			require.NoError(t, err)
			require.Len(t, checkpoints, 1)
			require.Contains(t, checkpoints[0].Reason, "pre-fs")
			require.Len(t, checkpoints[0].Entries, 1)
			_, err = manager.Restore(checkpoints[0].ID)
			require.NoError(t, err)
			raw, err = os.ReadFile(filepath.Join(workspace, "file.txt"))
			require.NoError(t, err)
			require.Equal(t, "before", string(raw))
			info, err := os.Stat(filepath.Join(workspace, "file.txt"))
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
			other, err := services.BuildSession(t.Context(), session.NewSession(t.TempDir()))
			require.NoError(t, err)
			defer other.Cleanup()
			response, err := runtimeTool(other.Tools, "checkpoint_restore", map[string]string{"id": checkpoints[0].ID})
			require.Error(t, err)
			_ = response
			receipt, err := json.Marshal(checkpoints[0])
			require.NoError(t, err)
			t.Logf("actual automatic pre-write checkpoint: %s", receipt)
		})
	}
}
