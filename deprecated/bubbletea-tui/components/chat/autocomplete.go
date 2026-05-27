// Package chat — inline `/command` autocomplete dropdown.
//
// Autocomplete is a small, non-modal hint shown beneath the editor when the
// first character is `/`. It filters a registered command list by substring
// match (case-insensitive) and lets the user pick via Up/Down + Tab/Enter.
package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
)

// AutocompleteEntry is a single suggestion (id is what gets inserted).
type AutocompleteEntry struct {
	ID    string
	Title string
	Desc  string
}

// Autocomplete is the dropdown model.
type Autocomplete struct {
	entries []AutocompleteEntry
	all     []AutocompleteEntry
	cursor  int
	visible bool
}

// NewAutocomplete builds a fresh dropdown over the given command universe.
func NewAutocomplete(entries []AutocompleteEntry) *Autocomplete {
	cp := append([]AutocompleteEntry(nil), entries...)
	return &Autocomplete{all: cp}
}

// SetEntries replaces the candidate set.
func (a *Autocomplete) SetEntries(entries []AutocompleteEntry) {
	a.all = append([]AutocompleteEntry(nil), entries...)
}

// Update recomputes suggestions for the current editor text. The dropdown is
// hidden if the text isn't `/`-prefixed or if there are no matches.
func (a *Autocomplete) Update(editorText string) {
	a.cursor = 0
	if !strings.HasPrefix(editorText, "/") {
		a.visible = false
		a.entries = nil
		return
	}
	q := strings.ToLower(strings.TrimPrefix(editorText, "/"))
	q = strings.TrimSpace(q)
	matches := make([]AutocompleteEntry, 0, len(a.all))
	for _, e := range a.all {
		if q == "" || strings.Contains(strings.ToLower(e.ID), q) {
			matches = append(matches, e)
		}
	}
	a.entries = matches
	a.visible = len(matches) > 0
}

// Visible reports whether the dropdown is showing.
func (a *Autocomplete) Visible() bool { return a.visible }

// Hide forcibly closes the dropdown (e.g., on Esc).
func (a *Autocomplete) Hide() {
	a.visible = false
	a.entries = nil
	a.cursor = 0
}

// Move shifts the highlight by delta entries (clamped).
func (a *Autocomplete) Move(delta int) {
	if !a.visible {
		return
	}
	a.cursor += delta
	if a.cursor < 0 {
		a.cursor = 0
	}
	if a.cursor >= len(a.entries) {
		a.cursor = len(a.entries) - 1
	}
}

// Selected returns the highlighted entry, or zero value if none.
func (a *Autocomplete) Selected() (AutocompleteEntry, bool) {
	if !a.visible || len(a.entries) == 0 {
		return AutocompleteEntry{}, false
	}
	return a.entries[a.cursor], true
}

// Completion returns the text that should replace the editor contents on Tab.
func (a *Autocomplete) Completion() (string, bool) {
	e, ok := a.Selected()
	if !ok {
		return "", false
	}
	return "/" + e.ID, true
}

// Entries returns the current suggestion list (read-only).
func (a *Autocomplete) Entries() []AutocompleteEntry {
	cp := make([]AutocompleteEntry, len(a.entries))
	copy(cp, a.entries)
	return cp
}

// Cursor returns the highlighted index.
func (a *Autocomplete) Cursor() int { return a.cursor }

// View renders the dropdown.
func (a *Autocomplete) View(width int) string {
	if !a.visible {
		return ""
	}
	t := theme.CurrentTheme()
	rowStyle := lipgloss.NewStyle().Foreground(t.Text())
	hiStyle := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Accent()).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(t.TextMuted())
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Border()).
		Padding(0, 1)

	w := width - 4
	if w < 20 {
		w = 20
	}
	var lines []string
	max := len(a.entries)
	if max > 8 {
		max = 8
	}
	for i := 0; i < max; i++ {
		e := a.entries[i]
		line := "/" + e.ID
		if e.Desc != "" {
			line += "  " + descStyle.Render(e.Desc)
		}
		if i == a.cursor {
			lines = append(lines, hiStyle.Render(line))
		} else {
			lines = append(lines, rowStyle.Render(line))
		}
	}
	return box.Width(w).Render(strings.Join(lines, "\n"))
}
