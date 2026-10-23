package main

import (
	"context"
	"errors"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hotreload"
	"github.com/spf13/cobra"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestCommandErrorJoinsApplicationWatcher(t *testing.T) {
	original := rootCmd
	defer func() { rootCmd = original; stopApplicationHotReload() }()
	fixture := errors.New("command fixture")
	var bus *hotreload.Bus
	rootCmd = &cobra.Command{Use: "fixture", SilenceErrors: true, SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		if err := startApplicationHotReload(cmd.Context(), t.TempDir()); err != nil {
			return err
		}
		bus = hotReloadBus
		return fixture
	}}
	rootCmd.SetArgs(nil)
	if err := executeRootCommand(context.Background()); !errors.Is(err, fixture) {
		t.Fatal(err)
	}
	if bus == nil {
		t.Fatal("watcher not started")
	}
	if err := bus.Start(context.Background()); !errors.Is(err, hotreload.ErrStopped) {
		t.Fatalf("watcher still active: %v", err)
	}
}

func TestApplicationWatcherConcurrentOwnership(t *testing.T) {
	defer stopApplicationHotReload()
	home := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := startApplicationHotReload(context.Background(), home); err != nil {
				t.Error(err)
			}
			stopApplicationHotReload()
		}()
	}
	wg.Wait()
	applicationReload.Lock()
	defer applicationReload.Unlock()
	if applicationReload.stop != nil {
		t.Fatal("watcher owner leaked")
	}
}

func TestInteractiveShellCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell cancellation fixture")
	}
	if testing.Short() {
		t.Skip("subprocess")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := execShellCommand(ctx, "sleep 30")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("shell descendants did not terminate promptly")
	}
}
