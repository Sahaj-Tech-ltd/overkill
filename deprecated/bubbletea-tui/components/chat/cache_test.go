package chat

import (
	"fmt"
	"strings"
	"testing"
)

func TestMessageCache_Hit(t *testing.T) {
	ClearCache()
	m := Message{ID: "test-1", Role: "user", Content: "hello"}
	v1 := m.View(80)
	v2 := m.View(80)
	if v1 != v2 {
		t.Error("same ID+width should return cached result")
	}
}

func TestMessageCache_Invalidate(t *testing.T) {
	ClearCache()
	m := Message{ID: "test-2", Role: "user", Content: strings.Repeat("x", 60)}
	v1 := m.View(80)
	v2 := m.View(40)
	if v1 == v2 {
		t.Error("different width should give different result")
	}
}

func TestMessageCache_Bounded(t *testing.T) {
	ClearCache()
	for i := 0; i < 105; i++ {
		m := Message{ID: fmt.Sprintf("msg-%d", i), Role: "user", Content: fmt.Sprintf("content %d", i)}
		m.View(80)
	}
	renderCache.RLock()
	count := len(renderCache.entries)
	renderCache.RUnlock()
	if count > maxCacheEntries {
		t.Errorf("cache should be bounded, got %d", count)
	}
}

func TestMessageCache_Clear(t *testing.T) {
	ClearCache()
	m := Message{ID: "clear-test", Role: "user", Content: "hello"}
	m.View(80)
	ClearCache()
	renderCache.RLock()
	count := len(renderCache.entries)
	renderCache.RUnlock()
	if count != 0 {
		t.Errorf("cache should be empty after clear, got %d", count)
	}
}
