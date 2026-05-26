package chat

import (
	"strconv"
	"testing"
)

// BenchmarkView_ShortMessages exercises the common case: a long
// session of short user/assistant exchanges. Most chat usage. Verifies
// culling keeps per-frame work flat as scrollback grows.
func BenchmarkView_ShortMessages(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run("messages="+strconv.Itoa(n), func(b *testing.B) {
			ml := NewMessageList()
			ml.SetSize(80, 40)
			for i := 0; i < n; i++ {
				ml.Append(NewMessage("user", "what is this"))
				ml.Append(NewMessage("assistant", "the answer"))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = ml.View()
			}
		})
	}
}

// BenchmarkView_TallMessages is the pathological case the old code
// handled badly: a few very tall messages. The old impl rendered every
// message in the message-count window regardless of cell budget;
// culling stops at the budget.
func BenchmarkView_TallMessages(b *testing.B) {
	ml := NewMessageList()
	ml.SetSize(80, 40)
	for i := 0; i < 50; i++ {
		ml.Append(NewMessage("assistant", repeatedLine(60)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ml.View()
	}
}

