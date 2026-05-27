package chat

import (
	"testing"
)

func TestMessageList_View(t *testing.T) {
	ml := NewMessageList()
	ml.SetSize(80, 10)
	ml.Append(NewMessage("user", "hello"))
	ml.Append(NewMessage("assistant", "world"))
	v := ml.View()
	if len(v) == 0 {
		t.Error("view should not be empty with messages")
	}
}
