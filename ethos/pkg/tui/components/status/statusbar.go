package status

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	tuitypes "github.com/Sahaj-Tech-ltd/ethos/pkg/tui/types"
)

type spinnerTickMsg struct{}

type StateChangeMsg struct {
	State    tuitypes.StatusState
	ToolName string
}

var spinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type StatusBarModel struct {
	width           int
	height          int
	state           tuitypes.StatusState
	modelName       string
	personalityMode string
	totalCost       float64
	inputTokens     int64
	outputTokens    int64
	contextPct      float64
	sessionName     string
	spinnerFrame    int
	toolName        string
}

func NewStatusBar() StatusBarModel {
	return StatusBarModel{
		state:     tuitypes.StatusIdle,
		modelName: "ethos",
		width:     80,
	}
}

func (m StatusBarModel) Init() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func (m StatusBarModel) Update(msg tea.Msg) (StatusBarModel, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerTickMsg:
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerChars)
		return m, tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
			return spinnerTickMsg{}
		})
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tuitypes.CostUpdateMsg:
		m.totalCost = msg.TotalCost
		m.inputTokens = msg.InputTokens
		m.outputTokens = msg.OutputTokens
		m.contextPct = msg.ContextPct
		return m, nil
	case tuitypes.PersonalityStateMsg:
		m.personalityMode = msg.Mode
		return m, nil
	case StateChangeMsg:
		m.state = msg.State
		m.toolName = msg.ToolName
		return m, nil
	case tuitypes.SessionLoadedMsg:
		if msg.Session != nil {
			m.sessionName = msg.Session.Title
		}
		return m, nil
	}
	return m, nil
}

func (m StatusBarModel) View() string {
	barStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e1e2e")).
		Foreground(lipgloss.Color("#cdd6f4"))

	left := m.renderLeft(barStyle)
	center := m.renderCenter(barStyle)
	right := m.renderRight()

	return lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
}

func (m StatusBarModel) getPersonalityText() string {
	switch m.personalityMode {
	case "off":
		return "·"
	case "subtle":
		return "~"
	case "witty":
		switch m.state {
		case tuitypes.StatusIdle:
			return "Chillaxin'"
		case tuitypes.StatusThinking:
			return "Pondering..."
		case tuitypes.StatusGenerating:
			return "Cooking..."
		default:
			return "Working..."
		}
	case "full":
		switch m.state {
		case tuitypes.StatusIdle:
			return "Ready to vibe!"
		case tuitypes.StatusThinking:
			return "Deep in thought..."
		case tuitypes.StatusGenerating:
			return "Creating magic..."
		default:
			return "On it!"
		}
	default:
		return ""
	}
}

func (m StatusBarModel) renderLeft(style lipgloss.Style) string {
	var stateText string
	if pt := m.getPersonalityText(); pt != "" {
		stateText = pt
	} else {
		switch m.state {
		case tuitypes.StatusIdle:
			stateText = "Ready"
		case tuitypes.StatusThinking:
			stateText = "Thinking..."
		case tuitypes.StatusGenerating:
			stateText = "Generating..."
		case tuitypes.StatusToolCall:
			stateText = "Tool: " + m.toolName
		}
	}

	spinner := ""
	if m.state != tuitypes.StatusIdle {
		spinner = spinnerChars[m.spinnerFrame] + " "
	}

	segment := spinner + stateText
	if m.sessionName != "" {
		segment += " | " + m.sessionName
	}

	w := m.width / 3
	return style.Width(w).Render(segment)
}

func (m StatusBarModel) renderCenter(style lipgloss.Style) string {
	segment := m.modelName
	if m.personalityMode != "" {
		segment += " | " + m.personalityMode
	}

	w := m.width / 3
	return style.Width(w).Align(lipgloss.Center).Render(segment)
}

func (m StatusBarModel) renderRight() string {
	fgColor := "#cdd6f4"
	if m.contextPct > 0.95 {
		fgColor = "#f38ba8"
	} else if m.contextPct > 0.80 {
		fgColor = "#f9e2af"
	}

	rightStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#1e1e2e")).
		Foreground(lipgloss.Color(fgColor))

	tokenStr := formatTokens(m.inputTokens)
	costStr := formatCost(m.totalCost)
	pctStr := formatPercent(m.contextPct)

	segment := fmt.Sprintf("%s | %s | %s", tokenStr, costStr, pctStr)
	w := m.width / 3
	return rightStyle.Width(w).Align(lipgloss.Right).Render(segment)
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000.0)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1000000.0)
}

func formatCost(c float64) string {
	return fmt.Sprintf("$%.2f", c)
}

func formatPercent(p float64) string {
	return fmt.Sprintf("%.0f%%", p*100)
}
