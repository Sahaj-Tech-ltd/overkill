package hotreload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func awaitPath(t *testing.T, ch <-chan Event, path string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Path == path {
				return
			}
		case <-deadline:
			t.Fatalf("no change for %s", path)
		}
	}
}

func TestAtomicReplacementRemainsWatched(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "user.yaml")
	b := runBus(t, Paths{UserConfigFile: file})
	ch, unsub := b.Subscribe(SubjectConfig)
	defer unsub()
	for i := 0; i < 3; i++ {
		temp := filepath.Join(home, "replacement")
		if err := os.WriteFile(temp, []byte("basic: {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temp, file); err != nil {
			t.Fatal(err)
		}
		awaitPath(t, ch, file)
	}
}

func TestMissingAncestorsAndExistingNestedDirectories(t *testing.T) {
	home := t.TempDir()
	skills := filepath.Join(home, "skills")
	nested := filepath.Join(skills, "one", "two")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(home, "missing", "deeper", "user.yaml")
	b := runBus(t, Paths{UserConfigFile: file, SkillsDir: skills})
	config, uc := b.Subscribe(SubjectConfig)
	defer uc()
	skill, us := b.Subscribe(SubjectSkill)
	defer us()
	child := filepath.Join(nested, "SKILL.md")
	if err := os.WriteFile(child, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitPath(t, skill, child)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("basic: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitPath(t, config, file)
	// The initial tree publication may arrive before recursive registration;
	// observing it guarantees later edits are watched too.
	if err := os.WriteFile(file, []byte("basic: {model: after}"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitPath(t, config, file)
}

func TestStopJoinsPendingTimersAndCannotRestart(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, "user.yaml")
	b := New(Paths{UserConfigFile: file})
	b.debounce = time.Second
	if err := b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ch, unsub := b.Subscribe(SubjectConfig)
	defer unsub()
	if err := os.WriteFile(file, []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b.Stop() }()
	}
	wg.Wait()
	if err := b.Start(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("restart: %v", err)
	}
	select {
	case e := <-ch:
		t.Fatalf("post-stop notification: %+v", e)
	case <-time.After(1100 * time.Millisecond):
	}
	b2 := New(Paths{})
	b2.Stop()
	if err := b2.Start(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatal(err)
	}
}
