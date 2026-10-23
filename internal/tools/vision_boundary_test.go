package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/vision"
	"github.com/stretchr/testify/require"
)

type boundaryVisionDescriber struct {
	calls int
	bytes []byte
}

func (d *boundaryVisionDescriber) Describe(_ context.Context, images []vision.Image, _ string) (string, error) {
	d.calls++
	d.bytes = append([]byte(nil), images[0].Bytes...)
	return "captured fixture", nil
}

func TestDefaultRegistryVisionWorkspaceBoundary(t *testing.T) {
	workspace, external := t.TempDir(), t.TempDir()
	inside := filepath.Join(workspace, "inside.png")
	outside := filepath.Join(external, "outside.png")
	pixels := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0}
	require.NoError(t, os.WriteFile(inside, pixels, 0600))
	require.NoError(t, os.WriteFile(outside, []byte("external harmless marker"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(workspace, "escape.png")))
	require.NoError(t, os.Symlink(external, filepath.Join(workspace, "escape-dir")))
	require.NoError(t, os.Symlink("inside.png", filepath.Join(workspace, "contained.png")))
	large, err := os.Create(filepath.Join(workspace, "large.png"))
	require.NoError(t, err)
	require.NoError(t, large.Truncate(maxVisionImageBytes+1))
	require.NoError(t, large.Close())
	for _, tc := range []struct {
		name, path string
		allow      bool
	}{
		{"relative", "inside.png", true}, {"absolute", inside, true}, {"contained symlink", "contained.png", true},
		{"external absolute", outside, false}, {"leaf escape", "escape.png", false}, {"directory escape", "escape-dir/outside.png", false},
		{"parent traversal", "../outside.png", false}, {"oversized", "large.png", false}, {"directory", ".", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &boundaryVisionDescriber{}
			registry := NewDefaultRegistry(FactoryDeps{CWD: workspace, VisionDescriber: d})
			tool, err := registry.Get("vision_describe")
			require.NoError(t, err)
			raw, err := json.Marshal(map[string]string{"file": tc.path})
			require.NoError(t, err)
			out, err := tool.Execute(t.Context(), raw)
			require.NoError(t, err)
			if tc.allow {
				require.Equal(t, 1, d.calls)
				require.Equal(t, pixels, d.bytes)
				require.Contains(t, string(out), "captured fixture")
			} else {
				require.Zero(t, d.calls)
				require.Contains(t, string(out), "error")
			}
		})
	}
}

func TestVisionFileFailsClosedWithoutRootOrAfterCancellation(t *testing.T) {
	d := &boundaryVisionDescriber{}
	tool := NewVisionDescribeTool(d, nil, BrowserHostPolicy{})
	out, err := tool.Execute(t.Context(), json.RawMessage(`{"file":"proof.png"}`))
	require.NoError(t, err)
	require.Contains(t, string(out), "requires a workspace root")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	tool.WithRootDir(t.TempDir())
	_, err = tool.Execute(ctx, json.RawMessage(`{"file":"proof.png"}`))
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, d.calls)
}
