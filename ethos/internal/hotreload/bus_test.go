package hotreload

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// runBus starts a Bus on a goroutine and returns a stop func that
// cancels Run + waits for exit. Cleans up via t.Cleanup.
func runBus(t *testing.T, paths Paths) *Bus {
	t.Helper()
	b := New(paths)
	// Faster debounce in tests so we don't pay 200ms per event.
	b.debounce = 30 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = b.Run(ctx) }()
	// Give the watcher a moment to register paths.
	time.Sleep(50 * time.Millisecond)
	t.Cleanup(func() {
		cancel()
		b.Stop()
	})
	return b
}

func TestBus_FiresOnFileWrite(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "user.yaml")
	if err := os.WriteFile(file, []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := runBus(t, Paths{UserConfigFile: file})
	ch, unsub := b.Subscribe(SubjectConfig)
	defer unsub()

	// Modify the file and expect an event.
	if err := os.WriteFile(file, []byte("schema_version: 1\nbasic:\n  vim_mode: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-ch:
		if ev.Subject != SubjectConfig {
			t.Errorf("Subject = %q, want config", ev.Subject)
		}
		if ev.Kind != EventModified {
			t.Errorf("Kind = %v, want EventModified", ev.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received within 2s")
	}
}

func TestBus_Debounce(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "user.yaml")
	_ = os.WriteFile(file, []byte("a"), 0o644)

	b := runBus(t, Paths{UserConfigFile: file})
	ch, unsub := b.Subscribe(SubjectConfig)
	defer unsub()

	// Burst of writes. Expect a SINGLE coalesced event.
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(file, []byte("v"+string(rune('a'+i))), 0o644)
		time.Sleep(5 * time.Millisecond)
	}

	got := 0
	timeout := time.After(300 * time.Millisecond)
loop:
	for {
		select {
		case <-ch:
			got++
		case <-timeout:
			break loop
		}
	}
	if got == 0 {
		t.Fatal("expected at least one debounced event")
	}
	if got > 2 {
		t.Errorf("expected debounce to coalesce; got %d events", got)
	}
}

func TestBus_ClassifyByPath(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	agentsDir := filepath.Join(dir, "agents")
	_ = os.MkdirAll(skillsDir, 0o755)
	_ = os.MkdirAll(agentsDir, 0o755)

	b := runBus(t, Paths{SkillsDir: skillsDir, AgentsDir: agentsDir})
	skillCh, unsubS := b.Subscribe(SubjectSkill)
	defer unsubS()
	agentCh, unsubA := b.Subscribe(SubjectSubagent)
	defer unsubA()

	// Drop a file in each dir.
	_ = os.WriteFile(filepath.Join(skillsDir, "foo.md"), []byte("# skill"), 0o644)
	_ = os.WriteFile(filepath.Join(agentsDir, "bar.md"), []byte("# agent"), 0o644)

	var gotSkill, gotAgent bool
	timeout := time.After(2 * time.Second)
	for !gotSkill || !gotAgent {
		select {
		case <-skillCh:
			gotSkill = true
		case <-agentCh:
			gotAgent = true
		case <-timeout:
			t.Fatalf("did not see both events: skill=%v agent=%v", gotSkill, gotAgent)
		}
	}
}

func TestBus_Unsubscribe(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "user.yaml")
	_ = os.WriteFile(file, []byte("x"), 0o644)

	b := runBus(t, Paths{UserConfigFile: file})
	ch, unsub := b.Subscribe(SubjectConfig)
	unsub()
	// After unsubscribe the channel is closed; reading should not
	// receive an event and must not block forever.
	_ = os.WriteFile(file, []byte("y"), 0o644)
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("received event after unsubscribe")
		}
	case <-time.After(200 * time.Millisecond):
		// Channel still open but no event — also acceptable, since
		// the fsnotify pipeline may have dropped due to no
		// subscriber. The point is: no panic, no deadlock.
	}
}

func TestBus_StopIdempotent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "user.yaml")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	b := runBus(t, Paths{UserConfigFile: file})
	b.Stop()
	b.Stop() // must not panic on second close
}
