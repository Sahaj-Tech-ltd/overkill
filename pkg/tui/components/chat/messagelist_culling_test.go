package chat

import (
	"strings"
	"testing"
)

// repeatedLine builds a payload that renders to roughly `lines` lines of
// text once the message wrapper adds its own framing. Adequate for
// asserting "this message is taller than X cells" without coupling tests
// to exact glamour formatting.
func repeatedLine(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		b.WriteString("line content here\n")
	}
	return b.String()
}

// linesIn returns the number of newline-separated lines in s. Single-
// line strings count as 1; trailing newlines are not double-counted.
func linesIn(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func TestView_CullsBeyondBudget(t *testing.T) {
	ml := NewMessageList()
	ml.SetSize(80, 12)
	// 5 messages × ~10 lines each = ~50 lines worth of content. The old
	// implementation would render all 5 because height was interpreted
	// as a message count; with culling we should fit far fewer.
	for i := 0; i < 5; i++ {
		ml.Append(NewMessage("user", repeatedLine(10)))
	}
	view := ml.View()
	got := linesIn(view)
	if got > 16 {
		t.Errorf("culled output exceeds budget+gap allowance: got %d lines, want <=16", got)
	}
	if got == 0 {
		t.Error("culling must still produce at least one message")
	}
}

func TestView_StickyToBottomOnAppend(t *testing.T) {
	ml := NewMessageList()
	ml.SetSize(80, 8)
	ml.Append(NewMessage("user", "first"))
	ml.Append(NewMessage("assistant", "second"))
	ml.Append(NewMessage("user", "latest"))
	view := ml.View()
	// The newest message should appear in the visible window. Old
	// behavior would compute offset = len-height and could leave the
	// final message off-screen when messages were tall.
	if !strings.Contains(view, "latest") {
		t.Errorf("newest message not visible after append:\n%s", view)
	}
}

func TestView_RendersAtLeastOneMessageEvenIfTaller(t *testing.T) {
	ml := NewMessageList()
	ml.SetSize(80, 4)
	// 30-line message into a 4-cell viewport. We deliberately render
	// it anyway — an empty viewport on a tall message would be more
	// confusing than overflow that the parent layout can clip.
	ml.Append(NewMessage("user", repeatedLine(30)))
	view := ml.View()
	if view == "" {
		t.Error("view should always render the current message even when oversize")
	}
}

func TestView_NoMessagesReturnsEmpty(t *testing.T) {
	ml := NewMessageList()
	ml.SetSize(80, 10)
	if got := ml.View(); got != "" {
		t.Errorf("empty list should render empty string, got %q", got)
	}
}

func TestView_OffsetClampedToValidRange(t *testing.T) {
	ml := NewMessageList()
	ml.SetSize(80, 8)
	ml.Append(NewMessage("user", "a"))
	ml.Append(NewMessage("user", "b"))
	// Manually push offset past the end — Append already does this via
	// the len(messages) sentinel, but verify direct manipulation can't
	// crash View.
	ml.offset = 99
	if v := ml.View(); v == "" {
		t.Error("View should clamp out-of-range offset, not bail empty")
	}
}

func TestRenderedHeight_OutOfRangeReturnsZero(t *testing.T) {
	ml := NewMessageList()
	ml.SetSize(80, 8)
	ml.Append(NewMessage("user", "hi"))
	if h := ml.renderedHeight(-1); h != 0 {
		t.Errorf("negative index: got %d, want 0", h)
	}
	if h := ml.renderedHeight(10); h != 0 {
		t.Errorf("past-end index: got %d, want 0", h)
	}
	if h := ml.renderedHeight(0); h <= 0 {
		t.Errorf("valid index should return positive height, got %d", h)
	}
}

// TestView_OldMessageCountSemanticsFixed pins the bug we fixed: with
// height=4 and 6 short messages, the old code would render all 6
// (because 6 > 4 messages so offset=2; rendered 4 messages of ~2 lines
// each = 8 lines, blowing through the 4-cell budget). New code respects
// cells, not message count.
func TestView_OldMessageCountSemanticsFixed(t *testing.T) {
	ml := NewMessageList()
	ml.SetSize(80, 4)
	for i := 0; i < 6; i++ {
		ml.Append(NewMessage("user", "short"))
	}
	view := ml.View()
	// 4 cells / (short message ≈ 1-3 lines + 2-line gap) → 1-2 messages.
	// Definitely should NOT be all 6.
	count := strings.Count(view, "you") // user messages render with "you" label
	if count >= 6 {
		t.Errorf("culling failed: %d messages visible in a 4-cell panel", count)
	}
}
