package status

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
	tuitypes "github.com/Sahaj-Tech-ltd/ethos/pkg/tui/types"
)

type spinnerTickMsg struct{}

type StateChangeMsg struct {
	State    tuitypes.StatusState
	ToolName string
}

var spinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type StatusBarModel struct {
	width        int
	height       int
	state        tuitypes.StatusState
	modelName    string
	provider     string
	totalCost    float64
	inputTokens  int64
	outputTokens int64
	contextPct   float64
	sessionName  string
	spinnerFrame int
	toolName     string
	cwd          string
	connState    int // 0 unknown, 1 ok, 2 retrying, 3 down
	mcpOK        int
	mcpFailed    int
	lspCount     int
	browserOn    bool
}

// SetBrowserActive toggles the [browser] indicator in the footer.
func (m *StatusBarModel) SetBrowserActive(on bool) {
	m.browserOn = on
}

// SetMCPCount updates the footer MCP indicator. Connected = green ⊙;
// any failed = red ⊙. Renders only when ok+failed > 0.
func (m *StatusBarModel) SetMCPCount(connected, failed int) {
	m.mcpOK = connected
	m.mcpFailed = failed
}

// SetLSPCount updates the footer LSP indicator. Renders only when count > 0.
func (m *StatusBarModel) SetLSPCount(n int) {
	m.lspCount = n
}

// SetConnState updates the connection-status indicator (the dot color).
func (m *StatusBarModel) SetConnState(s int) { m.connState = s }

func NewStatusBar() StatusBarModel {
	cwd, _ := os.Getwd()
	return StatusBarModel{
		state:     tuitypes.StatusIdle,
		modelName: "ethos",
		width:     80,
		cwd:       cwd,
	}
}

// SetModel updates the model + provider labels shown on the right side.
func (m *StatusBarModel) SetModel(model, provider string) {
	if model != "" {
		m.modelName = model
	}
	if provider != "" {
		m.provider = provider
	}
}

// spinnerTickInterval was 100ms which renders ~10x/s. Over SSH every render
// is a full screen of escape codes — this was a major source of perceived lag.
// 150ms was 6.7x/s, still wasteful. 200ms (5 fps) is smooth enough for a
// spinner indicator and saves ~33% more bandwidth vs 150ms, ~50% vs 100ms.
const spinnerTickInterval = 200 * time.Millisecond

func (m StatusBarModel) Init() tea.Cmd {
	// Don't start ticking until the agent is actually busy. The spinner is
	// only visible during non-idle states, so idle ticks are pure waste.
	return nil
}

func tickCmd() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func (m StatusBarModel) Update(msg tea.Msg) (StatusBarModel, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerTickMsg:
		// Stop ticking when idle — saves SSH bandwidth.
		if m.state == tuitypes.StatusIdle {
			return m, nil
		}
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerChars)
		return m, tickCmd()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tuitypes.CostUpdateMsg:
		m.totalCost = msg.TotalCost
		m.inputTokens = msg.InputTokens
		m.outputTokens = msg.OutputTokens
		m.contextPct = msg.ContextPct
		return m, nil
	case StateChangeMsg:
		prev := m.state
		m.state = msg.State
		m.toolName = msg.ToolName
		// Kick the spinner only on the idle→busy edge.
		if prev == tuitypes.StatusIdle && msg.State != tuitypes.StatusIdle {
			return m, tickCmd()
		}
		return m, nil
	case tuitypes.SessionLoadedMsg:
		if msg.Session != nil {
			m.sessionName = msg.Session.Title
		}
		return m, nil
	}
	return m, nil
}

// View renders an opencode-style single-line footer:
//
//	~/cwd/path                              ⠋ tool · • model · ⊙ provider · ↑↓42K · $0.12
func (m StatusBarModel) View() string {
	t := theme.CurrentTheme()

	left := m.renderLeft(t)
	right := m.renderRight(t)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := m.width - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	spacer := lipgloss.NewStyle().Foreground(t.TextMuted()).Render(strings.Repeat(" ", gap))

	bar := left + spacer + right
	return lipgloss.NewStyle().
		Background(t.StatusBarBackground()).
		Width(m.width).
		Render(bar)
}

func (m StatusBarModel) renderLeft(t theme.Theme) string {
	cwd := compactCwd(m.cwd)
	style := lipgloss.NewStyle().Foreground(t.TextMuted())
	return style.Render(cwd)
}

func (m StatusBarModel) renderRight(t theme.Theme) string {
	var parts []string

	if m.state != tuitypes.StatusIdle {
		spinner := lipgloss.NewStyle().Foreground(t.Primary()).Render(spinnerChars[m.spinnerFrame])
		label := strings.TrimSpace(m.toolName)
		if label == "" {
			label = stateLabel(m.state)
		}
		parts = append(parts, fmt.Sprintf("%s %s", spinner, lipgloss.NewStyle().Foreground(t.Text()).Render(label)))
	}

	// Connection dot defaults to muted until we observe a successful call.
	dotColor := t.TextMuted()
	switch m.connState {
	case 1:
		dotColor = t.Success()
	case 2:
		dotColor = t.Warning()
	case 3:
		dotColor = t.Error()
	}
	dot := lipgloss.NewStyle().Foreground(dotColor).Render("•")
	parts = append(parts, fmt.Sprintf("%s %s", dot, lipgloss.NewStyle().Foreground(t.Text()).Render(safeStr(m.modelName, "no model"))))

	if m.provider != "" {
		ring := lipgloss.NewStyle().Foreground(t.Success()).Render("⊙")
		parts = append(parts, fmt.Sprintf("%s %s", ring, lipgloss.NewStyle().Foreground(t.Text()).Render(m.provider)))
	}

	if m.mcpOK+m.mcpFailed > 0 {
		ringColor := t.Success()
		if m.mcpFailed > 0 {
			ringColor = t.Error()
		}
		ring := lipgloss.NewStyle().Foreground(ringColor).Render("⊙")
		parts = append(parts, fmt.Sprintf("%s %s", ring, lipgloss.NewStyle().Foreground(t.Text()).Render(fmt.Sprintf("%d MCP", m.mcpOK))))
	}

	if m.lspCount > 0 {
		dot := lipgloss.NewStyle().Foreground(t.Success()).Render("•")
		parts = append(parts, fmt.Sprintf("%s %s", dot, lipgloss.NewStyle().Foreground(t.Text()).Render(fmt.Sprintf("%d LSP", m.lspCount))))
	}

	if m.browserOn {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.Accent()).Render("[browser]"))
	}

	if m.inputTokens+m.outputTokens > 0 {
		tokColor := t.Text()
		if m.contextPct > 0.95 {
			tokColor = t.Error()
		} else if m.contextPct > 0.80 {
			tokColor = t.Warning()
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(tokColor).Render(
			fmt.Sprintf("↑↓%s", formatTokens(m.inputTokens+m.outputTokens)),
		))
	}

	if m.totalCost > 0 {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.Text()).Render(formatCost(m.totalCost)))
	}

	sep := lipgloss.NewStyle().Foreground(t.TextMuted()).Render(" · ")
	return strings.Join(parts, sep)
}

func stateLabel(s tuitypes.StatusState) string {
	switch s {
	case tuitypes.StatusThinking:
		return "thinking"
	case tuitypes.StatusGenerating:
		return "generating"
	case tuitypes.StatusToolCall:
		return "tool"
	default:
		return ""
	}
}

// compactCwd is called every render. Cache the last result so we don't
// re-stat HOME / re-clean the path on every keystroke. cwd changes are
// rare; the cache is single-entry which is plenty.
var (
	compactCwdLastIn  string
	compactCwdLastOut string
)

func compactCwd(cwd string) string {
	if cwd == "" {
		return ""
	}
	if cwd == compactCwdLastIn {
		return compactCwdLastOut
	}
	home := os.Getenv("HOME")
	var out string
	if home != "" && strings.HasPrefix(cwd, home) {
		out = "~" + strings.TrimPrefix(cwd, home)
	} else {
		out = filepath.Clean(cwd)
	}
	compactCwdLastIn = cwd
	compactCwdLastOut = out
	return out
}

func safeStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000.0)
	}
	return fmt.Sprintf("%.1fm", float64(n)/1000000.0)
}

func formatCost(c float64) string {
	return fmt.Sprintf("$%.2f", c)
}
