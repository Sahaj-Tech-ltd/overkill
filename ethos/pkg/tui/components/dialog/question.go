// Package dialog — agent-asks-user mid-turn overlay.
//
// Mirrors the permission dialog: the agent's QuestionFunc fires, ships a
// QuestionRequestMsg into the TUI loop, the dialog opens, the user answers,
// the answer flows back via a buffered channel.
package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
	tuitypes "github.com/Sahaj-Tech-ltd/ethos/pkg/tui/types"
)

// QuestionDecisionMsg is emitted after the user picks/types an answer.
type QuestionDecisionMsg struct {
	Text  string
	Index int
}

// QuestionDialog renders either a free-text input or a numbered choice list.
type QuestionDialog struct {
	Dialog
	Prompt  string
	Choices []string
	Cursor  int
	Input   string // free-text buffer
	Reply   chan<- tuitypes.QuestionReply
}

// NewQuestionDialog returns a fresh, hidden dialog.
func NewQuestionDialog() QuestionDialog {
	return QuestionDialog{Dialog: Dialog{Title: "agent question"}}
}

// SetRequest configures and shows the dialog.
func (q *QuestionDialog) SetRequest(req tuitypes.QuestionRequestMsg) {
	q.Prompt = req.Prompt
	q.Choices = append([]string(nil), req.Choices...)
	q.Reply = req.Reply
	q.Cursor = 0
	q.Input = ""
	q.Show = true
}

// Update handles arrow keys and selection.
func (q QuestionDialog) Update(msg tea.Msg) (QuestionDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return q, nil
	}
	if len(q.Choices) > 0 {
		return q.updateChoices(k)
	}
	return q.updateFreeText(k)
}

func (q QuestionDialog) updateChoices(k tea.KeyMsg) (QuestionDialog, tea.Cmd) {
	s := k.String()
	switch s {
	case "up", "k":
		if q.Cursor > 0 {
			q.Cursor--
		}
	case "down", "j":
		if q.Cursor < len(q.Choices)-1 {
			q.Cursor++
		}
	case "enter":
		idx := q.Cursor
		text := q.Choices[idx]
		q.send(tuitypes.QuestionReply{Text: text, Index: idx})
		q.Show = false
		return q, func() tea.Msg { return QuestionDecisionMsg{Text: text, Index: idx} }
	case "esc":
		q.send(tuitypes.QuestionReply{Index: -1, Cancel: true})
		q.Show = false
		return q, func() tea.Msg { return QuestionDecisionMsg{Index: -1} }
	default:
		// Number keys 1..9 for direct picks.
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			idx := int(s[0] - '1')
			if idx >= 0 && idx < len(q.Choices) {
				q.send(tuitypes.QuestionReply{Text: q.Choices[idx], Index: idx})
				q.Show = false
				return q, func() tea.Msg { return QuestionDecisionMsg{Text: q.Choices[idx], Index: idx} }
			}
		}
	}
	return q, nil
}

func (q QuestionDialog) updateFreeText(k tea.KeyMsg) (QuestionDialog, tea.Cmd) {
	switch k.String() {
	case "enter":
		text := q.Input
		q.send(tuitypes.QuestionReply{Text: text, Index: -1})
		q.Show = false
		return q, func() tea.Msg { return QuestionDecisionMsg{Text: text, Index: -1} }
	case "esc":
		q.send(tuitypes.QuestionReply{Index: -1, Cancel: true})
		q.Show = false
		return q, func() tea.Msg { return QuestionDecisionMsg{Index: -1} }
	case "backspace":
		if len(q.Input) > 0 {
			q.Input = q.Input[:len(q.Input)-1]
		}
	default:
		if len(k.Runes) > 0 {
			q.Input += string(k.Runes)
		}
	}
	return q, nil
}

func (q *QuestionDialog) send(r tuitypes.QuestionReply) {
	if q.Reply != nil {
		select {
		case q.Reply <- r:
		default:
		}
		q.Reply = nil
	}
}

// View renders the dialog.
func (q QuestionDialog) View(totalWidth, totalHeight int) string {
	if !q.Show {
		return ""
	}
	t := theme.CurrentTheme()
	hi := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Accent()).Bold(true)
	row := lipgloss.NewStyle().Foreground(t.Text())
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())

	var b strings.Builder
	b.WriteString(q.Prompt)
	b.WriteString("\n\n")
	if len(q.Choices) == 0 {
		b.WriteString("> " + q.Input + "▌\n\n")
		b.WriteString(muted.Render("enter: submit  ·  esc: cancel"))
	} else {
		for i, c := range q.Choices {
			line := fmt.Sprintf("%d. %s", i+1, c)
			if i == q.Cursor {
				b.WriteString(hi.Render("> " + line))
			} else {
				b.WriteString(row.Render("  " + line))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(muted.Render("number / arrows + enter  ·  esc: cancel"))
	}
	return q.BaseView(b.String(), totalWidth, totalHeight)
}
