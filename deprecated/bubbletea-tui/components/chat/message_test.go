package chat

import (
	"strings"
	"testing"
)

func TestMessage_UserRender(t *testing.T) {
	m := NewMessage("user", "hi")
	v := m.View(80)
	if !strings.Contains(v, "hi") {
		t.Error("should contain content")
	}
}

func TestMessage_AssistantRender(t *testing.T) {
	m := NewMessage("assistant", "hello")
	v := m.View(80)
	if !strings.Contains(v, "hello") {
		t.Error("should contain content")
	}
}

func TestMessage_ToolRender(t *testing.T) {
	m := NewMessage("tool", "ok")
	m.ToolName = "shell"
	v := m.View(80)
	if !strings.Contains(v, "shell") || !strings.Contains(v, "ok") {
		t.Error("missing tool info")
	}
}

func TestMessage_Truncation(t *testing.T) {
	m := NewMessage("user", strings.Repeat("x", 200))
	v := m.View(80)
	if len(v) == 0 {
		t.Error("empty view")
	}
}

func TestMessage_CacheKey(t *testing.T) {
	m := NewMessage("user", "test")
	m.Width = 80
	k1 := m.CacheKey()
	m.Width = 80
	k2 := m.CacheKey()
	if k1 != k2 {
		t.Error("same width should give same key")
	}
}

func TestMessage_EmptyContent(t *testing.T) {
	m := NewMessage("user", "")
	v := m.View(80)
	if v == "" {
		t.Error("should have placeholder")
	}
}

func TestMessageList_Append(t *testing.T) {
	ml := NewMessageList()
	ml.SetSize(80, 20)
	ml.Append(NewMessage("user", "a"))
	ml.Append(NewMessage("user", "b"))
	ml.Append(NewMessage("user", "c"))
	if ml.Len() != 3 {
		t.Errorf("expected 3, got %d", ml.Len())
	}
}
