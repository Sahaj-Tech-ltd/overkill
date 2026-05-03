// Package dialog — the discoverability help overlay.
//
// Categorized sections (Commands / Keybindings / Dialogs / Plugins / MCP /
// LSP / About) with a live-search input and keyboard navigation. Replaces
// the previous flat key-binding list.
package dialog

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// HelpEntry is one row inside a section. EntryAction is what the parent
// receives when the user presses Enter on a command/dialog entry.
type HelpEntry struct {
	Label   string
	Detail  string
	Section string
	Action  string // command id, dialog id, etc. Empty means non-actionable.
}

// HelpEntrySelectedMsg is emitted when the user activates an entry.
type HelpEntrySelectedMsg struct {
	Entry HelpEntry
}

// HelpAbout renders the version/build/license/docs section.
type HelpAbout struct {
	Version   string
	BuildDate string
	License   string
	DocsURL   string
}

// HelpDialog is the discoverable help overlay.
type HelpDialog struct {
	Dialog

	// Section sources — filled by the parent before showing.
	Commands  []Command
	Bindings  []key.Binding
	Dialogs   []HelpEntry
	Plugins   []HelpEntry
	MCP       []HelpEntry
	LSP       []HelpEntry
	About     HelpAbout

	// Search & navigation state.
	Query    string
	cursor   int
	entries  []HelpEntry // flattened, filtered list
}

func NewHelpDialog() HelpDialog {
	return HelpDialog{Dialog: Dialog{Title: "Help"}}
}

// SetBindings is kept for backwards compatibility with the old API.
func (h *HelpDialog) SetBindings(bindings []key.Binding) {
	h.Bindings = bindings
}

// SetCommands lets the parent feed in the slash command catalog.
func (h *HelpDialog) SetCommands(cmds []Command) {
	h.Commands = cmds
}

// SetDialogs feeds the catalog of openable dialogs.
func (h *HelpDialog) SetDialogs(entries []HelpEntry) {
	h.Dialogs = entries
}

// SetPlugins / SetMCP / SetLSP / SetAbout populate the dynamic sections.
func (h *HelpDialog) SetPlugins(entries []HelpEntry) { h.Plugins = entries }
func (h *HelpDialog) SetMCP(entries []HelpEntry)     { h.MCP = entries }
func (h *HelpDialog) SetLSP(entries []HelpEntry)     { h.LSP = entries }
func (h *HelpDialog) SetAbout(a HelpAbout)           { h.About = a }

// Update handles search input + navigation + dismiss.
func (h *HelpDialog) Update(msg tea.Msg) (*HelpDialog, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			h.Show = false
			h.Query = ""
			h.cursor = 0
			return h, func() tea.Msg { return CloseHelpMsg{} }
		case "?":
			// `?` toggles closed too — matches the legacy contract.
			h.Show = false
			h.Query = ""
			h.cursor = 0
			return h, func() tea.Msg { return CloseHelpMsg{} }
		case "down":
			h.flatten()
			if h.cursor < len(h.entries)-1 {
				h.cursor++
			}
			return h, nil
		case "up":
			if h.cursor > 0 {
				h.cursor--
			}
			return h, nil
		case "enter":
			h.flatten()
			if h.cursor >= 0 && h.cursor < len(h.entries) {
				e := h.entries[h.cursor]
				if e.Action != "" {
					h.Show = false
					return h, func() tea.Msg { return HelpEntrySelectedMsg{Entry: e} }
				}
			}
			return h, nil
		case "backspace":
			if len(h.Query) > 0 {
				h.Query = h.Query[:len(h.Query)-1]
				h.cursor = 0
			}
			return h, nil
		case "pgdown":
			h.flatten()
			h.cursor = h.jumpNextSection(h.cursor)
			return h, nil
		case "pgup":
			h.flatten()
			h.cursor = h.jumpPrevSection(h.cursor)
			return h, nil
		default:
			if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
				h.Query += string(msg.Runes)
				h.cursor = 0
				return h, nil
			}
		}
	case ShowHelpMsg:
		h.Show = true
		h.Query = ""
		h.cursor = 0
	case CloseHelpMsg:
		h.Show = false
	}
	return h, nil
}

// flatten rebuilds the visible-entries list from the current sources +
// query. Called lazily on every key event so we don't need to track a
// dirty flag.
func (h *HelpDialog) flatten() {
	q := strings.ToLower(strings.TrimSpace(h.Query))
	matches := func(label, detail string) bool {
		if q == "" {
			return true
		}
		return strings.Contains(strings.ToLower(label), q) ||
			strings.Contains(strings.ToLower(detail), q)
	}

	var out []HelpEntry
	addSection := func(name string, items []HelpEntry) {
		var kept []HelpEntry
		for _, it := range items {
			if matches(it.Label, it.Detail) {
				it.Section = name
				kept = append(kept, it)
			}
		}
		out = append(out, kept...)
	}

	// Commands
	cmdEntries := make([]HelpEntry, 0, len(h.Commands))
	for _, c := range h.Commands {
		cmdEntries = append(cmdEntries, HelpEntry{
			Label:  c.Title,
			Detail: c.Description,
			Action: c.ID,
		})
	}
	addSection("Commands", cmdEntries)

	// Keybindings
	kbEntries := make([]HelpEntry, 0, len(h.Bindings))
	for _, b := range h.Bindings {
		kbEntries = append(kbEntries, HelpEntry{
			Label:  b.Help().Key,
			Detail: b.Help().Desc,
		})
	}
	addSection("Keybindings", kbEntries)

	addSection("Dialogs", h.Dialogs)
	addSection("Plugins", h.Plugins)
	addSection("MCP", h.MCP)
	addSection("LSP", h.LSP)

	// About — synthesize as a few non-actionable entries.
	about := []HelpEntry{
		{Label: "version", Detail: h.About.Version},
		{Label: "build", Detail: h.About.BuildDate},
		{Label: "license", Detail: h.About.License},
		{Label: "docs", Detail: h.About.DocsURL},
	}
	addSection("About", about)

	if h.cursor >= len(out) {
		h.cursor = max(0, len(out)-1)
	}
	h.entries = out
}

// jumpNextSection moves the cursor to the first entry of the next section.
func (h *HelpDialog) jumpNextSection(cur int) int {
	if cur >= len(h.entries) {
		return cur
	}
	curSection := h.entries[cur].Section
	for i := cur + 1; i < len(h.entries); i++ {
		if h.entries[i].Section != curSection {
			return i
		}
	}
	return cur
}

// jumpPrevSection moves the cursor to the first entry of the previous section.
func (h *HelpDialog) jumpPrevSection(cur int) int {
	if cur <= 0 {
		return 0
	}
	curSection := h.entries[cur].Section
	// Step backwards until we cross a section boundary, then continue stepping
	// to the *first* entry of that section.
	i := cur - 1
	for i >= 0 && h.entries[i].Section == curSection {
		i--
	}
	if i < 0 {
		return 0
	}
	target := h.entries[i].Section
	for i > 0 && h.entries[i-1].Section == target {
		i--
	}
	return i
}

func (h HelpDialog) View(totalWidth, totalHeight int) string {
	if !h.Show {
		return ""
	}
	t := theme.CurrentTheme()
	(&h).flatten()

	const rowWidth = 60
	headerStyle := lipgloss.NewStyle().Foreground(t.DialogAccent()).Bold(true).Underline(true)
	rowStyle := lipgloss.NewStyle().Width(rowWidth).Foreground(t.DialogText())
	cursorStyle := lipgloss.NewStyle().
		Width(rowWidth).
		Foreground(t.DialogBackground()).
		Background(t.DialogAccent()).
		Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Italic(true)

	searchPrefix := lipgloss.NewStyle().Foreground(t.DialogAccent()).Render("/")
	searchLine := searchPrefix + " " + h.Query
	if h.Query == "" {
		searchLine = searchPrefix + " " + mutedStyle.Render("type to filter…")
	}

	maxRows := totalHeight - 10
	if maxRows > 18 {
		maxRows = 18
	}
	if maxRows < 6 {
		maxRows = 6
	}

	var lines []string
	lines = append(lines, searchLine, "")

	if len(h.entries) == 0 {
		lines = append(lines, mutedStyle.Render("  no matches"))
	} else {
		// Build rendered rows with section headers inline so windowing keeps
		// the cursor centered consistently.
		rendered := make([]string, 0, len(h.entries)+8)
		// Track which absolute indexes are entry rows (so cursor highlight maps).
		entryIdx := make([]int, 0, len(h.entries))
		var lastSection string
		for i, e := range h.entries {
			if e.Section != lastSection {
				if lastSection != "" {
					rendered = append(rendered, "")
				}
				rendered = append(rendered, headerStyle.Render(e.Section))
				lastSection = e.Section
			}
			text := fmt.Sprintf("  %-18s %s", e.Label, e.Detail)
			if i == h.cursor {
				rendered = append(rendered, cursorStyle.Render(text))
			} else {
				rendered = append(rendered, rowStyle.Render(text))
			}
			entryIdx = append(entryIdx, len(rendered)-1)
		}

		// Window centered on the rendered row of the cursor.
		anchor := 0
		if h.cursor >= 0 && h.cursor < len(entryIdx) {
			anchor = entryIdx[h.cursor]
		}
		visible, before, after := Window(rendered, anchor, maxRows)
		if before > 0 {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("  ↑ %d more", before)))
		}
		lines = append(lines, visible...)
		if after > 0 {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("  ↓ %d more", after)))
		}
	}

	lines = append(lines, "", mutedStyle.Render("  enter: run  ·  ↑/↓: nav  ·  pgup/pgdn: section  ·  esc: close"))

	return h.BaseView(strings.Join(lines, "\n"), totalWidth, totalHeight)
}
