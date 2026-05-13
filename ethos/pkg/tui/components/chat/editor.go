package chat

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
	tuitypes "github.com/Sahaj-Tech-ltd/overkill/pkg/tui/types"
)

// EditorModel is the prompt input. It owns a textarea plus side-cars for
// history recall and inline `/command` autocomplete.
type EditorModel struct {
	ta      textarea.Model
	focused bool
	width   int
	height  int

	history      *History
	autocomplete *Autocomplete
}

func NewEditor() EditorModel {
	ta := textarea.New()
	ta.Placeholder = "ask overkill anything, or / for commands"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0

	return EditorModel{
		ta:           ta,
		focused:      false,
		width:        40,
		height:       3,
		history:      NewHistory(),
		autocomplete: NewAutocomplete(nil),
	}
}

func (e EditorModel) Init() tea.Cmd {
	return nil
}

func (e *EditorModel) Focus() tea.Cmd {
	e.focused = true
	return e.ta.Focus()
}

func (e *EditorModel) Blur() tea.Cmd {
	e.focused = false
	e.ta.Blur()
	return nil
}

func (e EditorModel) IsFocused() bool {
	return e.focused
}

// SetHistory installs a history sidecar. Callers use this to swap in a
// session-persisted history when a session is opened.
func (e *EditorModel) SetHistory(h *History) {
	if h == nil {
		h = NewHistory()
	}
	e.history = h
}

// History exposes the underlying history (for parent wiring & tests).
func (e *EditorModel) History() *History { return e.history }

// SetAutocompleteEntries replaces the slash-command universe used for
// inline completion.
func (e *EditorModel) SetAutocompleteEntries(entries []AutocompleteEntry) {
	e.autocomplete.SetEntries(entries)
	e.autocomplete.Update(e.ta.Value())
}

// Autocomplete exposes the dropdown so the parent can render or interrogate it.
func (e *EditorModel) Autocomplete() *Autocomplete { return e.autocomplete }

func (e EditorModel) Update(msg tea.Msg) (EditorModel, tea.Cmd) {
	var cmd tea.Cmd

	if k, ok := msg.(tea.KeyMsg); ok {
		// Autocomplete-aware navigation (only when dropdown is visible).
		if e.autocomplete.Visible() {
			switch k.String() {
			case "up":
				e.autocomplete.Move(-1)
				return e, nil
			case "down":
				e.autocomplete.Move(1)
				return e, nil
			case "tab":
				if c, ok := e.autocomplete.Completion(); ok {
					e.ta.SetValue(c)
					e.autocomplete.Hide()
				}
				return e, nil
			case "enter":
				if !k.Alt {
					if c, ok := e.autocomplete.Completion(); ok {
						e.ta.SetValue(c)
						e.autocomplete.Hide()
						val := e.ta.Value()
						e.ta.SetValue("")
						e.history.Append(val)
						return e, func() tea.Msg { return tuitypes.SendMsg{Text: val} }
					}
				}
			case "esc":
				e.autocomplete.Hide()
				return e, nil
			}
		}

		switch k.String() {
		case "enter":
			if !k.Alt {
				val := e.ta.Value()
				if val != "" {
					e.ta.SetValue("")
					e.history.Append(val)
					e.autocomplete.Hide()
					return e, func() tea.Msg {
						return tuitypes.SendMsg{Text: val}
					}
				}
			}
		case "up":
			// History recall when the editor is empty OR we're already walking history.
			if e.ta.Value() == "" || e.history.IsActive() {
				if recall := e.history.Prev(); recall != "" {
					e.ta.SetValue(recall)
					return e, nil
				}
			}
		case "down":
			if e.history.IsActive() {
				recall := e.history.Next()
				e.ta.SetValue(recall)
				return e, nil
			}
		case "esc":
			if e.ta.Value() != "" {
				e.ta.SetValue("")
				e.history.Reset()
				e.autocomplete.Hide()
				return e, nil
			}
			return e, nil
		default:
			// Any rune key while in recall mode resets the cursor — the user
			// is starting a fresh edit on top of the recalled text.
			if e.history.IsActive() && len(k.Runes) > 0 {
				e.history.Reset()
			}
		}
	}

	e.ta, cmd = e.ta.Update(msg)
	// Keep the autocomplete dropdown in sync after every keystroke.
	if _, ok := msg.(tea.KeyMsg); ok {
		e.autocomplete.Update(e.ta.Value())
	}
	return e, cmd
}

func (e EditorModel) View() string {
	t := theme.CurrentTheme()
	border := t.BorderUnfocused()
	if e.focused {
		border = t.BorderFocused()
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1)
	w := e.width - 4
	if w < 10 {
		w = 10
	}
	style = style.Width(w)
	body := style.Render(e.ta.View())
	if e.autocomplete.Visible() {
		body = lipgloss.JoinVertical(lipgloss.Left, body, e.autocomplete.View(e.width))
	}
	return body
}

func (e EditorModel) Value() string {
	return e.ta.Value()
}

func (e *EditorModel) SetValue(s string) {
	e.ta.SetValue(s)
}

func (e *EditorModel) SetSize(w, h int) {
	e.width = w
	e.height = h
	inner := w - 4
	if inner < 10 {
		inner = 10
	}
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}
	e.ta.SetWidth(inner)
	e.ta.SetHeight(innerH)
}
