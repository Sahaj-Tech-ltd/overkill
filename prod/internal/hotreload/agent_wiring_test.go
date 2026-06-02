package hotreload

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeAgent satisfies the Agent interface used by WireAgent.
type fakeAgent struct {
	mu    sync.Mutex
	model string
}

func (f *fakeAgent) SetModel(m string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.model = m
}
func (f *fakeAgent) Model() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.model
}

// captureReporter records every reload outcome.
type captureReporter struct {
	mu      sync.Mutex
	changes [][]string
	errors  []error
}

func (c *captureReporter) OnReload(changed []string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.changes = append(c.changes, append([]string(nil), changed...))
	c.errors = append(c.errors, err)
}

func (c *captureReporter) all() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]string, len(c.changes))
	for i, ch := range c.changes {
		out[i] = append([]string(nil), ch...)
	}
	return out
}

func TestWireAgent_ModelHotSwap(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "user.yaml")
	if err := os.WriteFile(file, []byte("schema_version: 1\nbasic:\n  model: claude-opus-4-7\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	b := runBus(t, Paths{UserConfigFile: file})
	agent := &fakeAgent{model: "claude-opus-4-7"}
	rep := &captureReporter{}

	ctx, cancel := context.WithCancel(context.Background())
	stop, err := WireAgent(ctx, b, agent, file, rep)
	if err != nil {
		cancel()
		t.Fatalf("WireAgent: %v", err)
	}
	// Defer in cancel-first order: cancel fires ctx.Done() so the
	// WireAgent goroutine exits and stop()'s <-done unblocks.
	defer stop()
	defer cancel()

	// Save a new model.
	if err := os.WriteFile(file, []byte("schema_version: 1\nbasic:\n  model: claude-haiku-4-5\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Poll for the agent to receive the new model. fsnotify + debounce
	// + the apply goroutine round-trip — give it up to 1s.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if agent.Model() == "claude-haiku-4-5" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := agent.Model(); got != "claude-haiku-4-5" {
		t.Fatalf("agent model = %q, want claude-haiku-4-5", got)
	}

	// Reporter should have at least one entry containing basic.model.
	changes := rep.all()
	if len(changes) == 0 {
		t.Fatal("reporter saw no reloads")
	}
	found := false
	for _, ch := range changes {
		for _, f := range ch {
			if f == "basic.model" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected basic.model in reported changes, got %v", changes)
	}
}

func TestWireAgent_PersonaChangeReports(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "user.yaml")
	_ = os.WriteFile(file, []byte("schema_version: 1\n"), 0o600)

	b := runBus(t, Paths{UserConfigFile: file})
	agent := &fakeAgent{}
	rep := &captureReporter{}
	ctx, cancel := context.WithCancel(context.Background())
	stop, _ := WireAgent(ctx, b, agent, file, rep)
	defer stop()
	defer cancel()

	_ = os.WriteFile(file, []byte("schema_version: 1\nadvanced:\n  persona:\n    tone: terse\n"), 0o600)

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		for _, ch := range rep.all() {
			for _, f := range ch {
				if f == "advanced.persona" {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("did not see advanced.persona reload; got %v", rep.all())
}
