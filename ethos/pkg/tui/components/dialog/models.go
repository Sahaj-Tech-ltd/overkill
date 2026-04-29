package dialog

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type ModelEntry struct {
	ID       string
	Name     string
	Provider string
}

type ShowModelDialogMsg struct{}
type CloseModelDialogMsg struct{}
type ModelSelectedMsg struct{ ModelID string }

type ModelDialog struct {
	Dialog
	Models         []ModelEntry
	Filtered       []ModelEntry
	Cursor         int
	Query          string
	Providers      []string
	ActiveProvider int
}

func NewModelDialog() ModelDialog {
	return ModelDialog{Dialog: Dialog{Title: "Models"}}
}

func (m *ModelDialog) SetModels(models []ModelEntry) {
	m.Models = models
	m.Filtered = models
	m.Cursor = 0
	m.Query = ""
	m.Providers = nil
	m.ActiveProvider = 0
	seen := map[string]bool{}
	for _, mdl := range models {
		if !seen[mdl.Provider] {
			m.Providers = append(m.Providers, mdl.Provider)
			seen[mdl.Provider] = true
		}
	}
}

func (m ModelDialog) Update(msg tea.Msg) (ModelDialog, tea.Cmd) {
	switch k := msg.(type) {
	case tea.KeyMsg:
		switch k.String() {
		case "up":
			if m.Cursor > 0 {
				m.Cursor--
			}
		case "down":
			if m.Cursor < len(m.Filtered)-1 {
				m.Cursor++
			}
		case "tab":
			if len(m.Providers) > 0 {
				m.ActiveProvider = (m.ActiveProvider + 1) % len(m.Providers)
				m.filterByProvider()
			}
		case "enter":
			if m.Cursor < len(m.Filtered) {
				return m, func() tea.Msg { return ModelSelectedMsg{ModelID: m.Filtered[m.Cursor].ID} }
			}
		case "esc":
			return m, func() tea.Msg { return CloseModelDialogMsg{} }
		default:
			if len(k.Runes) > 0 && k.Type == tea.KeyRunes {
				m.Query += string(k.Runes)
				m.filter()
			}
		}
	case ShowModelDialogMsg:
		m.Show = true
	case CloseModelDialogMsg:
		m.Show = false
		m.Query = ""
		m.Cursor = 0
	}
	return m, nil
}

func (m *ModelDialog) filter() {
	m.Filtered = nil
	for _, mdl := range m.Models {
		if strings.Contains(strings.ToLower(mdl.Name), strings.ToLower(m.Query)) ||
			strings.Contains(strings.ToLower(mdl.Provider), strings.ToLower(m.Query)) {
			m.Filtered = append(m.Filtered, mdl)
		}
	}
	if m.Cursor >= len(m.Filtered) {
		m.Cursor = max(0, len(m.Filtered)-1)
	}
}

func (m *ModelDialog) filterByProvider() {
	if len(m.Providers) == 0 {
		return
	}
	provider := m.Providers[m.ActiveProvider]
	m.Filtered = nil
	for _, mdl := range m.Models {
		if mdl.Provider == provider {
			m.Filtered = append(m.Filtered, mdl)
		}
	}
	m.Cursor = 0
}

func (m ModelDialog) View(totalWidth, totalHeight int) string {
	if !m.Show {
		return ""
	}
	var lines []string
	if len(m.Providers) > 0 {
		var tabs []string
		for i, p := range m.Providers {
			if i == m.ActiveProvider {
				tabs = append(tabs, "["+p+"]")
			} else {
				tabs = append(tabs, " "+p+" ")
			}
		}
		lines = append(lines, strings.Join(tabs, " "))
		lines = append(lines, "")
	}
	for i, mdl := range m.Filtered {
		name := mdl.Name
		if len(name) > 40 {
			name = name[:37] + "..."
		}
		if i == m.Cursor {
			lines = append(lines, "> "+name)
		} else {
			lines = append(lines, "  "+name)
		}
	}
	if len(m.Filtered) == 0 {
		lines = append(lines, "No models found")
	}
	content := strings.Join(lines, "\n")
	return m.BaseView(content, totalWidth, totalHeight)
}
