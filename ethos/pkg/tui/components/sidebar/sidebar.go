package sidebar

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

const MinSidebarWidth = 20

type Panel interface {
	View(width, height int) string
	Name() string
}

type SidebarModel struct {
	width     int
	height    int
	activeTab int
	panels    []Panel
	tabNames  []string
	focused   bool
}

func NewSidebar() SidebarModel {
	return SidebarModel{
		tabNames: []string{"Cost", "Files", "Session"},
	}
}

func (m *SidebarModel) SetPanels(panels []Panel) {
	m.panels = panels
	names := make([]string, len(panels))
	for i, p := range panels {
		names[i] = p.Name()
	}
	if len(names) > 0 {
		m.tabNames = names
	}
}

func (m SidebarModel) ActivePanel() Panel {
	if len(m.panels) == 0 || m.activeTab >= len(m.panels) {
		return nil
	}
	return m.panels[m.activeTab]
}

func (m SidebarModel) Init() tea.Cmd {
	return nil
}

func (m SidebarModel) Update(msg tea.Msg) (SidebarModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "tab" {
			m.activeTab = (m.activeTab + 1) % len(m.tabNames)
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}
	return m, nil
}

func (m SidebarModel) View() string {
	if m.width < MinSidebarWidth {
		return ""
	}

	t := theme.CurrentTheme()

	tabBar := m.renderTabBar(t)

	contentHeight := m.height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}

	var content string
	if m.ActivePanel() != nil {
		content = m.ActivePanel().View(m.width-2, contentHeight)
	} else {
		noDataStyle := lipgloss.NewStyle().
			Foreground(t.TextMuted()).
			Width(m.width - 2).
			Align(lipgloss.Center).
			Height(contentHeight)
		content = noDataStyle.Render("No data")
	}

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.SidebarBorder()).
		Background(t.SidebarBackground()).
		Width(m.width).
		Height(m.height)

	return borderStyle.Render(lipgloss.JoinVertical(lipgloss.Left, tabBar, content))
}

func (m SidebarModel) renderTabBar(t theme.Theme) string {
	var parts []string
	for i, name := range m.tabNames {
		if i == m.activeTab {
			activeStyle := lipgloss.NewStyle().
				Foreground(t.SidebarActiveTab()).
				Bold(true)
			parts = append(parts, activeStyle.Render(name))
		} else {
			inactiveStyle := lipgloss.NewStyle().
				Foreground(t.SidebarInactiveTab())
			parts = append(parts, inactiveStyle.Render(name))
		}
	}

	separator := lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Render(" │ ")

	return strings.Join(parts, separator)
}

func (m *SidebarModel) Focus() tea.Cmd {
	m.focused = true
	return nil
}

func (m *SidebarModel) Blur() tea.Cmd {
	m.focused = false
	return nil
}

func (m SidebarModel) IsFocused() bool {
	return m.focused
}

func (m *SidebarModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m SidebarModel) GetSize() (int, int) {
	return m.width, m.height
}
