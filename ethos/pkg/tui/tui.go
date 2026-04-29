package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/internal/personality"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/components/dialog"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/components/sidebar"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/components/status"
)

const defaultSidebarWidth = 30

type appModel struct {
	width       int
	height      int
	showSidebar bool
	sidebar     sidebar.SidebarModel

	boot   BootModel
	person *personality.Personality

	statusBar status.StatusBarModel
	toast     status.ToastModel

	quit     dialog.QuitDialog
	help     dialog.HelpDialog
	models   dialog.ModelDialog
	sessions dialog.SessionDialog
	commands dialog.CommandDialog
	showQuit bool
	showHelp bool
	showModels   bool
	showSessions bool
	showCommands bool

	pageView   func() string
	pageUpdate func(msg tea.Msg) tea.Cmd
	pageInit   func() tea.Cmd
}

func New(app *App) tea.Model {
	sb := sidebar.NewSidebar()
	m := &appModel{
		sidebar:      sb,
		showSidebar:  true,
		quit:         dialog.NewQuitDialog(),
		help:         dialog.NewHelpDialog(),
		models:       dialog.NewModelDialog(),
		sessions:     dialog.NewSessionDialog(),
		commands:     dialog.NewCommandDialog(),
		boot:         NewBootModel(),
		statusBar:    status.NewStatusBar(),
		toast:        status.NewToastModel(),
		pageView:     func() string { return "" },
		pageUpdate:   func(msg tea.Msg) tea.Cmd { return nil },
		pageInit:     func() tea.Cmd { return nil },
	}

	m.help.SetBindings([]key.Binding{
		Keys.Quit, Keys.Help, Keys.SwitchSession, Keys.Commands, Keys.Models,
	})

	if app != nil {
		m.applyApp(app)
	}

	return m
}

func (m *appModel) applyApp(app *App) {
	if app.Config != nil {
		var lvl personality.Level
		switch app.Config.Personality.Level {
		case "witty":
			lvl = personality.LevelWitty
		case "full":
			lvl = personality.LevelFull
		case "off":
			lvl = personality.LevelOff
		default:
			lvl = personality.LevelSubtle
		}
		m.person = personality.New(personality.Config{
			AgentName: app.Config.Agent.Name,
			Level:     lvl,
		})
		m.boot.person = m.person
	}

	costPanel := sidebar.NewCostPanel()
	filesPanel := sidebar.NewFilesPanel()
	sessionPanel := sidebar.NewSessionPanel()
	m.sidebar.SetPanels([]sidebar.Panel{&costPanel, &filesPanel, &sessionPanel})

	if app.Agent != nil {
		if id := app.Agent.SessionID(); id != "" {
			sessionPanel.SetCurrent(id)
		}
	}
}

func (m *appModel) SetPage(view func() string, update func(tea.Msg) tea.Cmd, init func() tea.Cmd) {
	m.pageView = view
	m.pageUpdate = update
	m.pageInit = init
}

func (m *appModel) Init() tea.Cmd {
	return tea.Batch(
		m.pageInit(),
		m.statusBar.Init(),
		m.toast.Init(),
		LoadBootData(m.person),
	)
}

func (m *appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if m.boot.visible {
		switch msg.(type) {
		case BootCompleteMsg, tea.KeyMsg:
			cmd := m.boot.Update(msg)
			return m, cmd
		}
	}

	anyOverlay := m.showQuit || m.showHelp || m.showModels || m.showSessions || m.showCommands

	if dialog.BlockKeys(msg, anyOverlay) {
		if m.showQuit {
			updated, cmd := m.quit.Update(msg)
			m.quit = *updated
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
		if m.showHelp {
			updated, cmd := m.help.Update(msg)
			m.help = *updated
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
		if m.showModels {
			updated, cmd := m.models.Update(msg)
			m.models = updated
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
		if m.showSessions {
			updated, cmd := m.sessions.Update(msg)
			m.sessions = updated
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
		if m.showCommands {
			updated, cmd := m.commands.Update(msg)
			m.commands = updated
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height - 1

		if m.showSidebar && m.width >= 60 {
			m.sidebar.SetSize(defaultSidebarWidth, m.height)
		}

		cmds = append(cmds, m.pageUpdate(msg))

		var statusCmd tea.Cmd
		m.statusBar, statusCmd = m.statusBar.Update(tea.WindowSizeMsg{Width: m.width})
		cmds = append(cmds, statusCmd)
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		if key.Matches(msg, Keys.Quit) {
			if m.showQuit {
				m.showQuit = false
				return m, func() tea.Msg { return dialog.CloseQuitMsg{} }
			}
			m.showQuit = true
			updated, cmd := m.quit.Update(dialog.ShowQuitMsg{})
			m.quit = *updated
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
		if key.Matches(msg, Keys.Help) {
			if m.showHelp {
				m.showHelp = false
				return m, func() tea.Msg { return dialog.CloseHelpMsg{} }
			}
			m.showHelp = true
			updated, cmd := m.help.Update(dialog.ShowHelpMsg{})
			m.help = *updated
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
		if key.Matches(msg, Keys.Models) {
			if m.showModels {
				m.showModels = false
				return m, func() tea.Msg { return dialog.CloseModelDialogMsg{} }
			}
			m.showModels = true
			m.models.Show = true
			return m, nil
		}
		if key.Matches(msg, Keys.SwitchSession) {
			if m.showSessions {
				m.showSessions = false
				return m, func() tea.Msg { return dialog.CloseSessionDialogMsg{} }
			}
			m.showSessions = true
			m.sessions.Show = true
			return m, nil
		}
		if key.Matches(msg, Keys.Commands) {
			if m.showCommands {
				m.showCommands = false
				return m, func() tea.Msg { return dialog.CloseCommandDialogMsg{} }
			}
			m.showCommands = true
			m.commands.Show = true
			return m, nil
		}
	}

	switch msg := msg.(type) {
	case dialog.CloseQuitMsg:
		m.showQuit = false
		updated, _ := m.quit.Update(msg)
		m.quit = *updated
	case dialog.CloseHelpMsg:
		m.showHelp = false
		updated, _ := m.help.Update(msg)
		m.help = *updated
	case dialog.CloseModelDialogMsg:
		m.showModels = false
		m.models.Update(msg)
	case dialog.CloseSessionDialogMsg:
		m.showSessions = false
		m.sessions.Update(msg)
	case dialog.CloseCommandDialogMsg:
		m.showCommands = false
		m.commands.Update(msg)
	}

	cmds = append(cmds, m.pageUpdate(msg))

	var statusCmd tea.Cmd
	m.statusBar, statusCmd = m.statusBar.Update(msg)
	cmds = append(cmds, statusCmd)

	var toastCmd tea.Cmd
	m.toast, toastCmd = m.toast.Update(msg)
	cmds = append(cmds, toastCmd)

	sd, cmd := m.sidebar.Update(msg)
	m.sidebar = sd
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *appModel) View() string {
	if m.boot.visible {
		return m.boot.View()
	}

	if m.width <= 0 {
		return ""
	}

	pageView := m.pageView()

	if m.showSidebar && m.width >= 60 {
		sidebarView := m.sidebar.View()
		if sidebarView != "" {
			pageView = lipgloss.JoinHorizontal(lipgloss.Top, pageView, sidebarView)
		}
	}

	statusView := m.statusBar.View()
	toastView := m.toast.View()

	mainView := lipgloss.JoinVertical(lipgloss.Top, pageView, statusView)

	if toastView != "" {
		mainView = lipgloss.JoinVertical(lipgloss.Left, mainView, toastView)
	}

	if m.showQuit {
		return m.quit.View(m.width, m.height)
	}
	if m.showHelp {
		return m.help.View(m.width, m.height)
	}
	if m.showModels {
		return m.models.View(m.width, m.height)
	}
	if m.showSessions {
		return m.sessions.View(m.width, m.height)
	}
	if m.showCommands {
		return m.commands.View(m.width, m.height)
	}

	return mainView
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
