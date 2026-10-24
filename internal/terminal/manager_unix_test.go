//go:build unix

package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func manager(t *testing.T, options Options) *Manager {
	t.Helper()
	m, err := New(t.TempDir(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m
}

var mainOwner = Owner{Session: "chat", Worker: "main"}

func waitFor(t *testing.T, m *Manager, owner Owner, id string, predicate func(Chunk) bool) Chunk {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		chunk, err := m.Read(ctx, owner, id, false, 0, 64*1024, 100*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(chunk) {
			return chunk
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTerminalPersistsAcrossCallContextsAndRetainsFinalOutput(t *testing.T) {
	m := manager(t, Options{})
	launch, cancel := context.WithCancel(context.Background())
	info, err := m.Start(launch, mainOwner, `read value; printf 'answer:%s\n' "$value"`, "", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	cancel() // Ending the launching tool call must not kill the terminal.
	if _, err := m.Write(context.Background(), mainOwner, info.ID, false, []byte("persisted\n")); err != nil {
		t.Fatal(err)
	}
	out := waitFor(t, m, mainOwner, info.ID, func(c Chunk) bool { return c.Info.State == "exited" })
	if !strings.Contains(out.Output, "answer:persisted") || out.Info.ExitCode == nil || *out.Info.ExitCode != 0 {
		t.Fatalf("lost terminal output: %+v", out)
	}
	resumed, err := m.Read(context.Background(), mainOwner, info.ID, false, out.Cursor, 4096, 0)
	if err != nil || len(resumed.Data) != 0 {
		t.Fatalf("cursor replayed old output: %+v %v", resumed, err)
	}
}
func TestOwnersAndHumanTakeover(t *testing.T) {
	m := manager(t, Options{})
	child := Owner{Session: "chat", Worker: "backend-attempt"}
	sibling := Owner{Session: "chat", Worker: "frontend-attempt"}
	info, err := m.Start(context.Background(), child, "cat", "", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []Owner{sibling, {Session: "different-chat", Worker: "main"}} {
		if _, err := m.Write(context.Background(), owner, info.ID, false, []byte("foreign\n")); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign writer admitted", err)
		}
		if _, err := m.Read(context.Background(), owner, info.ID, false, 0, 1024, 0); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign read admitted", err)
		}
	}
	if len(m.List(mainOwner, false)) != 0 || len(m.List(mainOwner, true)) != 1 {
		t.Fatal("incorrect parent/human visibility")
	}
	if err := m.TakeControl(child, info.ID, false, false); err == nil {
		t.Fatal("agent stole human control")
	}
	if err := m.TakeControl(mainOwner, info.ID, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write(context.Background(), child, info.ID, false, []byte("agent\n")); !errors.Is(err, ErrHumanControl) {
		t.Fatal(err)
	}
	if _, err := m.Write(context.Background(), mainOwner, info.ID, true, []byte("human\n")); err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(mainOwner, info.ID, true, 40, 120); err != nil {
		t.Fatal(err)
	}
	if err := m.TakeControl(mainOwner, info.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write(context.Background(), child, info.ID, false, []byte("agent returned\n")); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseTerminal(mainOwner, info.ID, true); err != nil {
		t.Fatal(err)
	}
}
func TestBoundedCursorOutputAndRetentionLimit(t *testing.T) {
	m := manager(t, Options{BufferBytes: 64, MaxRetained: 1})
	info, err := m.Start(context.Background(), mainOwner, "yes x | head -c 8192", "", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	out := waitFor(t, m, mainOwner, info.ID, func(c Chunk) bool { return c.Info.State == "exited" })
	if len(out.Data) > 64 || !out.Truncated || out.Earliest == 0 || out.Cursor != out.Earliest+uint64(len(out.Data)) {
		t.Fatalf("bad bounded cursor: %+v", out)
	}
	if _, err := m.Start(context.Background(), mainOwner, "echo another", "", 24, 80); err == nil {
		t.Fatal("retained limit bypassed")
	}
	if err := m.Forget(mainOwner, info.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), mainOwner, "echo another", "", 24, 80); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Read(context.Background(), mainOwner, info.ID, false, 0, 1024, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("forgotten handle resolved")
	}
}
func TestTerminalCWDAndManagerShutdown(t *testing.T) {
	m := manager(t, Options{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(m.workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{outside, "escape"} {
		if _, err := m.Start(context.Background(), mainOwner, "echo forbidden", cwd, 24, 80); err == nil {
			t.Fatal("outside cwd admitted")
		}
	}
	info, err := m.Start(context.Background(), mainOwner, "cat", "", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write(context.Background(), mainOwner, info.ID, false, []byte("late")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, err := m.Start(context.Background(), mainOwner, "echo late", "", 24, 80); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
func TestLifetimeAndOwnerCleanup(t *testing.T) {
	m := manager(t, Options{MaxLifetime: 80 * time.Millisecond})
	info, err := m.Start(context.Background(), mainOwner, "cat", "", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, m, mainOwner, info.ID, func(c Chunk) bool { return c.Info.State == "closed" })
	child := Owner{Session: "chat", Worker: "child"}
	info, err = m.Start(context.Background(), child, "cat", "", 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CloseOwner(child); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Write(context.Background(), child, info.ID, false, []byte("late")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
