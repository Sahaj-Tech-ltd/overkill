package chat

import (
	"testing"
)

func TestEditor_Focus(t *testing.T) {
	e := NewEditor()
	cmd := e.Focus()
	if !e.IsFocused() {
		t.Error("editor should be focused")
	}
	_ = cmd
}

func TestEditor_Blur(t *testing.T) {
	e := NewEditor()
	e.Focus()
	cmd := e.Blur()
	if e.IsFocused() {
		t.Error("editor should not be focused after blur")
	}
	_ = cmd
}

func TestEditor_SetValue(t *testing.T) {
	e := NewEditor()
	e.SetValue("hello")
	if e.Value() != "hello" {
		t.Errorf("expected 'hello', got '%s'", e.Value())
	}
}

func TestEditor_View(t *testing.T) {
	e := NewEditor()
	v := e.View()
	if len(v) == 0 {
		t.Error("view should not be empty")
	}
}
