package skills

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const watchTestSkillV1 = `---
name: hot-reload-target
version: 1.0.0
description: Skill used to exercise the fsnotify hot-reload watcher end to end
author: test
category: util
tags: [hot, reload]
triggers: [hot, reload]
enabled: true
---

# Hot Reload Target

Initial body.
`

const watchTestSkillV2 = `---
name: hot-reload-target
version: 1.1.0
description: Skill used to exercise the fsnotify hot-reload watcher end to end
author: test
category: util
tags: [hot, reload]
triggers: [hot, reload]
enabled: true
---

# Hot Reload Target

Updated body.
`

// collector captures onChange callbacks in order. Callers wait on its channel
// with a timeout long enough to absorb fsnotify + debounce latency.
type collector struct {
	mu     sync.Mutex
	events []Skill
	ch     chan Skill
}

func newCollector() *collector {
	return &collector{ch: make(chan Skill, 16)}
}

func (c *collector) fn(s Skill) {
	c.mu.Lock()
	c.events = append(c.events, s)
	c.mu.Unlock()
	select {
	case c.ch <- s:
	default:
	}
}

func (c *collector) wait(t *testing.T, d time.Duration) Skill {
	t.Helper()
	select {
	case s := <-c.ch:
		return s
	case <-time.After(d):
		t.Fatalf("timed out waiting for onChange after %v", d)
		return Skill{}
	}
}

func TestLoaderWatch_CreateModifyDelete(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	bundledDir := t.TempDir() // empty bundled dir is fine

	loader := NewLoader(bundledDir, userDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	col := newCollector()
	require.NoError(t, loader.Watch(ctx, col.fn))

	// Allow the watcher's goroutine to install fsnotify before we start writing.
	time.Sleep(50 * time.Millisecond)

	path := filepath.Join(userDir, "hot-reload-target.md")

	// 1. Create — onChange fires with parsed skill.
	require.NoError(t, os.WriteFile(path, []byte(watchTestSkillV1), 0o600))
	got := col.wait(t, 2*time.Second)
	require.Equal(t, "hot-reload-target", got.Name)
	require.Equal(t, "1.0.0", got.Version)
	require.True(t, got.Enabled)

	// 2. Modify — onChange fires again with the new content.
	// Sleep slightly longer than the debounce so we get a distinct event.
	time.Sleep(watchDebounce + 100*time.Millisecond)
	require.NoError(t, os.WriteFile(path, []byte(watchTestSkillV2), 0o600))
	got = col.wait(t, 2*time.Second)
	require.Equal(t, "hot-reload-target", got.Name)
	require.Equal(t, "1.1.0", got.Version)

	// 3. Delete — onChange fires with Enabled=false so callers drop the skill.
	time.Sleep(watchDebounce + 100*time.Millisecond)
	require.NoError(t, os.Remove(path))
	got = col.wait(t, 2*time.Second)
	require.Equal(t, "hot-reload-target", got.Name, "delete event should preserve last-known name")
	require.False(t, got.Enabled, "delete event must set Enabled=false")
}

func TestLoaderWatch_CtxCancelStopsWatcher(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	loader := NewLoader("", userDir)

	ctx, cancel := context.WithCancel(context.Background())
	col := newCollector()
	require.NoError(t, loader.Watch(ctx, col.fn))

	time.Sleep(50 * time.Millisecond)
	cancel()

	// After cancel + a generous grace window the watcher must be silent even
	// when we drop a new skill file into the watched directory.
	time.Sleep(watchDebounce + 200*time.Millisecond)

	path := filepath.Join(userDir, "post-cancel.md")
	require.NoError(t, os.WriteFile(path, []byte(watchTestSkillV1), 0o600))

	select {
	case s := <-col.ch:
		t.Fatalf("watcher fired after ctx cancel: %+v", s)
	case <-time.After(watchDebounce + 300*time.Millisecond):
		// expected — watcher stopped cleanly
	}
}

func TestLoaderWatch_IgnoresNonSkillFiles(t *testing.T) {
	t.Parallel()

	userDir := t.TempDir()
	loader := NewLoader("", userDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	col := newCollector()
	require.NoError(t, loader.Watch(ctx, col.fn))
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(userDir, "notes.txt"), []byte("hello"), 0o644))

	select {
	case s := <-col.ch:
		t.Fatalf("watcher fired for non-skill file: %+v", s)
	case <-time.After(watchDebounce + 200*time.Millisecond):
		// expected
	}
}
