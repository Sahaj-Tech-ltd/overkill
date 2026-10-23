package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestResolveFrontendInstalledOutsideSource(t *testing.T) {
	t.Setenv("OVERKILL_TUI_DIR", "")
	root := t.TempDir()
	executable := filepath.Join(root, "Overkill.app", "Contents", "MacOS", "overkill")
	frontend := filepath.Join(root, "Overkill.app", "Contents", "Resources", "tui")
	require.NoError(t, os.MkdirAll(filepath.Join(frontend, "dist"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(frontend, "dist", "entry.js"), []byte("// fixture"), 0644))
	for _, isGo := range []bool{false, true} {
		cwd := t.TempDir()
		if isGo {
			require.NoError(t, os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module example.org/unrelated\n"), 0644))
		}
		got, err := resolveFrontend(executable, cwd)
		require.NoError(t, err)
		require.Equal(t, frontend, got)
	}
}

func TestResolveFrontendExplicitConfiguration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OVERKILL_TUI_DIR", dir)
	_, err := resolveFrontend("/installed/overkill", t.TempDir())
	require.ErrorContains(t, err, "OVERKILL_TUI_DIR")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "entry.tsx"), nil, 0644))
	got, err := resolveFrontend("/installed/overkill", t.TempDir())
	require.NoError(t, err)
	require.Equal(t, dir, got)
}

func TestWaitAPIReady(t *testing.T) {
	t.Run("slow startup", func(t *testing.T) {
		ready := make(chan struct{})
		result := make(chan error, 1)
		go func() { time.Sleep(600 * time.Millisecond); close(ready) }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, waitAPIReady(ctx, ready, result))
	})
	t.Run("bind failure", func(t *testing.T) {
		result := make(chan error, 1)
		result <- errors.New("listen: address already in use")
		require.ErrorContains(t, waitAPIReady(context.Background(), make(chan struct{}), result), "address already in use")
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.ErrorIs(t, waitAPIReady(ctx, make(chan struct{}), make(chan error)), context.Canceled)
	})
}

func TestAPIPort(t *testing.T) {
	require.Equal(t, "8432", apiPort("http://127.0.0.1:8432"))
	require.Equal(t, "8432", apiPort("http://[::1]:8432"))
	require.Empty(t, apiPort("invalid"))
}

func TestLaunchFrontendPreservesExitStatus(t *testing.T) {
	server := api.NewServer(api.ServerConfig{Config: config.Default()})
	frontend := exec.Command("/bin/sh", "-c", "exit 23")
	err := launchFrontend(context.Background(), server, frontend)
	require.ErrorContains(t, err, "exit status 23")
	_, httpErr := (&http.Client{Timeout: time.Second}).Get(server.Addr() + "/health")
	require.Error(t, httpErr, "API listener must be closed before returning")
}

func TestLaunchFrontendReportsExecFailure(t *testing.T) {
	server := api.NewServer(api.ServerConfig{Config: config.Default()})
	err := launchFrontend(context.Background(), server, exec.Command(filepath.Join(t.TempDir(), "missing")))
	require.ErrorContains(t, err, "launch terminal frontend")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLaunchFrontendReplacesInheritedConnectionEnvironment(t *testing.T) {
	server := api.NewServer(api.ServerConfig{Config: config.Default()})
	t.Setenv("OVERKILL_API_URL", "invalid-inherited-url")
	t.Setenv("OVERKILL_API_PORT", "invalid-inherited-port")
	t.Setenv("OVERKILL_API_TOKEN", "invalid-inherited-token")
	frontend := exec.Command("/bin/sh", "-c", `test -n "$OVERKILL_API_TOKEN" && test "$OVERKILL_API_TOKEN" != invalid-inherited-token && test "$OVERKILL_API_URL" != invalid-inherited-url && test "$OVERKILL_API_PORT" != invalid-inherited-port`)
	require.NoError(t, launchFrontend(context.Background(), server, frontend))
}
