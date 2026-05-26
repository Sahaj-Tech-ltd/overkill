package chat

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// messageGap is the blank-line separator between rendered messages.
// Two newlines = one blank line between bubbles. Counted into the
// viewport budget so culling doesn't render a message whose body would
// fit but whose separator would push it off-screen.
const messageGap = 2

// maxRetainedMessages caps the in-memory transcript so a long-lived
// TUI doesn't leak hundreds of MB of message strings. Anything older
// than this drops off the front of the slice on Append. The viewport
// only ever shows the tail, so culling the head is invisible. The
// session journal preserves the full history on disk for /export.
const maxRetainedMessages = 500

type MessageListModel struct {
	messages []Message
	offset   int
	width    int
	height   int
}

func NewMessageList() MessageListModel {
	return MessageListModel{}
}

func (m MessageListModel) Init() tea.Cmd {
	return nil
}

func (m MessageListModel) Len() int {
	return len(m.messages)
}

func (m *MessageListModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *MessageListModel) Append(msg Message) {
	m.messages = append(m.messages, msg)
	// Cap retained messages — without this, an all-day session held
	// every token of every reply in RAM as Go strings. We drop the
	// oldest in chunks (rather than one-at-a-time) to amortise the
	// slice copy. Journal store still has the full history; this is
	// purely the TUI working set.
	if len(m.messages) > maxRetainedMessages {
		drop := len(m.messages) - maxRetainedMessages
		// Copy down into a fresh slice so the underlying array can be
		// GC'd. Re-slicing would keep the old array alive.
		trimmed := make([]Message, maxRetainedMessages)
		copy(trimmed, m.messages[drop:])
		m.messages = trimmed
	}
	// Auto-scroll to the bottom on append. The exact offset is computed
	// lazily in View() based on rendered line counts — we just signal
	// "stick to bottom" with a sentinel large enough that View clamps it
	// back to the right index. Doing the height math here would double
	// the render work (count once on append, count again on View).
	m.offset = len(m.messages)
}

// UpdateLastAssistant rewrites the most recent assistant message in place. Used
// while streaming a response so we don't append a new bubble per token.
func (m *MessageListModel) UpdateLastAssistant(content string) {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" {
			m.messages[i].Content = content
			m.messages[i].Streaming = true
			return
		}
	}
}

// FinalizeLastAssistant marks the most recent assistant message as no longer
// streaming so the next render uses the markdown renderer instead of plain
// text. Called once on stream Done.
func (m *MessageListModel) FinalizeLastAssistant() {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Role == "assistant" {
			m.messages[i].Streaming = false
			return
		}
	}
}

func (m MessageListModel) Update(msg tea.Msg) (MessageListModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if m.offset > 0 {
				m.offset--
			}
		case "down":
			maxOff := maxOffset(len(m.messages), m.height)
			if m.offset < maxOff {
				m.offset++
			}
		}
	}
	return m, nil
}

// View renders the visible window of messages. Two things happen here
// that didn't in the old implementation:
//
//  1. Cell-aware culling. The old code treated m.height as "messages
//     that fit" — for a 40-cell panel and 40 tall messages, it would
//     render ~2000 lines and let the parent layout discard them. We
//     now walk messages from offset forward and stop once their
//     rendered line counts exceed the panel budget.
//
//  2. Bottom-stickiness. Append() sets offset to len(messages) as a
//     "stick to bottom" sentinel. View() clamps that back to whichever
//     index lets the last message fit fully — picking the offset by
//     scanning backwards until the cumulative height fills the panel.
//
// All Message.View calls are cache hits after the first frame for a
// given (id, width, content-len, streaming) tuple, so the line-count
// pass is essentially a hash lookup per message.
func (m MessageListModel) View() string {
	if len(m.messages) == 0 {
		return ""
	}
	budget := max(1, m.height)

	// Clamp offset to a valid index. Append() sets it to len(messages)
	// as a stick-to-bottom signal; we resolve that here by scanning
	// backwards from the end until the cumulative rendered height
	// exceeds the budget, then nudging forward by one so the last
	// message stays fully visible.
	last := len(m.messages) - 1
	if m.offset > last {
		m.offset = last
		used := 0
		for i := last; i >= 0; i-- {
			h := m.renderedHeight(i)
			if i < last {
				used += messageGap
			}
			if used+h > budget {
				m.offset = i + 1
				if m.offset > last {
					m.offset = last
				}
				break
			}
			used += h
			m.offset = i
		}
	}
	if m.offset < 0 {
		m.offset = 0
	}

	// Reset the click-zone registry at the start of every frame. Stale
	// zones from a prior render (e.g. before a scroll) would otherwise
	// hit-test positive against locations now occupied by different
	// content.
	ResetZones()

	// Forward render pass: include messages from offset until the
	// cumulative height exceeds the panel. The very first message is
	// always included even if it overflows — the outer layout can clip
	// what doesn't fit, but a totally-empty viewport on a tall message
	// would be confusing.
	var rendered []string
	used := 0
	for i := m.offset; i < len(m.messages); i++ {
		view := m.messages[i].View(m.width)
		h := strings.Count(view, "\n") + 1
		gap := 0
		if len(rendered) > 0 {
			gap = messageGap
		}
		if len(rendered) > 0 && used+gap+h > budget {
			break
		}
		// Track the absolute row this message starts at before we
		// commit it to the output. used+gap is exactly the row index
		// of the first line of this message within the visible window.
		topRow := used + gap
		rendered = append(rendered, view)
		used += gap + h

		// Register any copy chips on this message's footer row.
		// The chips render on the LAST line of the message body
		// (appendCopyFooter appended them after the cached render),
		// so footer_row = topRow + h - 1.
		registerCopyChips(m.messages[i], m.width, topRow+h-1)
	}

	return strings.Join(rendered, "\n\n")
}

// registerCopyChips pushes one CopyZone per chip on the message's
// footer row into the global registry. Called per visible message
// during View so the registry exactly matches what's on screen.
func registerCopyChips(msg Message, width, footerRow int) {
	chips := msg.CopyChips(width, HoveredID())
	if len(chips) == 0 {
		return
	}
	for _, c := range chips {
		RegisterZone(CopyZone{
			Row:  footerRow,
			MinX: c.ColStart,
			MaxX: c.ColEnd,
			Body: c.Body,
			Lang: c.Lang,
		})
	}
}

// renderedHeight returns the line count of message i at the current
// width. Hits the same render cache the View method uses, so
// computing heights for the scrollback is free after the first frame.
func (m MessageListModel) renderedHeight(i int) int {
	if i < 0 || i >= len(m.messages) {
		return 0
	}
	return strings.Count(m.messages[i].View(m.width), "\n") + 1
}

// maxOffset is retained for callers that need a quick upper bound but
// is no longer used by View — kept for backwards compatibility with
// keybinding handlers that paginate by message count.
func maxOffset(totalMessages, height int) int {
	fit := max(1, height)
	if totalMessages <= fit {
		return 0
	}
	return totalMessages - fit
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
