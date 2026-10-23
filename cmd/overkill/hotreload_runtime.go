package main

import (
	"context"
	"log"
	"path/filepath"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/hotreload"
)

var applicationReload struct {
	sync.Mutex
	stop func()
}

// Application ownership spans the complete command, including startup errors.
// Individual agents own only their subscriptions, never the shared watcher.
func startApplicationHotReload(ctx context.Context, home string) error {
	applicationReload.Lock()
	defer applicationReload.Unlock()
	stopApplicationHotReloadLocked()
	if home == "" {
		return nil
	}
	bus := hotreload.New(hotreload.Paths{
		SkillsDir: filepath.Join(home, "skills"), AgentsDir: filepath.Join(home, "agents"),
		PluginsDir: filepath.Join(home, "plugins"), UserConfigFile: filepath.Join(home, "user.yaml"),
	})
	lifetime, cancel := context.WithCancel(ctx)
	if err := bus.Start(lifetime); err != nil {
		cancel()
		bus.Stop()
		return err
	}
	done := make(chan struct{})
	reporter := hotreload.NewLogReporter(log.Printf, "application")
	go func() {
		defer close(done)
		for {
			select {
			case <-lifetime.Done():
				return
			case err := <-bus.Errors():
				reporter.OnReload(nil, err)
			}
		}
	}()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); bus.Stop(); <-done }) }
	applicationReload.stop = stop
	hotReloadBus = bus
	return nil
}

func stopApplicationHotReload() {
	applicationReload.Lock()
	defer applicationReload.Unlock()
	stopApplicationHotReloadLocked()
}

func stopApplicationHotReloadLocked() {
	stop := applicationReload.stop
	applicationReload.stop = nil
	hotReloadBus = nil
	if stop != nil {
		stop()
	}
}

func executeRootCommand(ctx context.Context) error {
	defer stopApplicationHotReload()
	return rootCmd.ExecuteContext(ctx)
}
