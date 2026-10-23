//go:build unix

package localdb

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeHelperOwner(t *testing.T) {
	if os.Getenv("OVERKILL_LOCALDB_HELPER") != "1" {
		t.Skip("subprocess fixture")
	}
	m, err := NewManager(Config{BinDir: os.Getenv("OVERKILL_POSTGRES_BIN_DIR"), DataDir: os.Getenv("OVERKILL_LOCALDB_HELPER_DATA")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("OWNED_DATABASE_READY")
	select {}
}

func TestNativeRecoversAuthenticatedOrphanAfterParentCrash(t *testing.T) {
	cfg := nativeConfig(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfg.DataDir), 0o700))
	executable, err := os.Executable()
	require.NoError(t, err)
	child := exec.Command(executable, "-test.run=^TestNativeHelperOwner$")
	child.Env = append(os.Environ(), "OVERKILL_LOCALDB_HELPER=1", "OVERKILL_LOCALDB_HELPER_DATA="+cfg.DataDir)
	output, err := child.StdoutPipe()
	require.NoError(t, err)
	child.Stderr = os.Stderr
	require.NoError(t, child.Start())
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			if scanner.Text() == "OWNED_DATABASE_READY" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	t.Cleanup(func() {
		_ = child.Process.Kill()
		// This cleanup owns only the fresh test cluster, including failure paths.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, binary(cfg.BinDir, "pg_ctl"), "-D", cfg.DataDir, "-m", "immediate", "-w", "stop").Run()
	})
	select {
	case ok := <-ready:
		require.True(t, ok)
	case <-time.After(30 * time.Second):
		t.Fatal("helper startup timed out")
	}
	canonical, err := filepath.EvalSymlinks(cfg.DataDir)
	require.NoError(t, err)
	prior, err := readPostmaster(canonical)
	require.NoError(t, err)
	require.NoError(t, child.Process.Kill())
	require.Error(t, child.Wait())
	require.NoError(t, syscall.Kill(prior.PID, 0), "owned PostgreSQL remains alive after application SIGKILL")
	m, info := startNative(t, cfg)
	after, err := readPostmaster(canonical)
	require.NoError(t, err)
	require.NotEqual(t, prior.PID, after.PID, "recovered authenticated orphan is shut down before a new lifetime")
	db := connectNative(t, info)
	db.Close()
	require.NoError(t, m.Stop(t.Context()))
}
