package sidebar

import (
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

type SessionPanel struct {
	sessions  []*session.Session
	currentID string
	width     int
	height    int
}

func NewSessionPanel() SessionPanel {
	return SessionPanel{}
}

func (p SessionPanel) Name() string {
	return "Session"
}

func (p *SessionPanel) SetSessions(sessions []*session.Session) {
	p.sessions = sessions
}

func (p *SessionPanel) SetCurrent(id string) {
	p.currentID = id
}

func (p SessionPanel) View(width, height int) string {
	if width <= 0 {
		width = 30
	}
	if height <= 0 {
		height = 15
	}

	if len(p.sessions) == 0 {
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
		return dim.Render("No sessions")
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#89b4fa"))
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6adc8"))

	var lines []string

	var current *session.Session
	for _, s := range p.sessions {
		if s.ID == p.currentID {
			current = s
			break
		}
	}

	if current != nil {
		lines = append(lines, headerStyle.Render("Current Session"))
		title := truncate(current.Title, width-4)
		lines = append(lines, labelStyle.Render(title))
		lines = append(lines, valueStyle.Render(formatRelativeTime(current.CreatedAt)))
		lines = append(lines, valueStyle.Render(fmt.Sprintf("%d turns", current.TurnCount)))
		lines = append(lines, "")
	}

	lines = append(lines, headerStyle.Render("Recent"))

	sorted := make([]*session.Session, len(p.sessions))
	copy(sorted, p.sessions)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UpdatedAt.After(sorted[j].UpdatedAt)
	})

	maxShow := 5
	if len(sorted) < maxShow {
		maxShow = len(sorted)
	}

	bulletStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))

	for i := 0; i < maxShow; i++ {
		s := sorted[i]
		availWidth := width - 4
		if availWidth < 5 {
			availWidth = 5
		}

		name := truncate(s.Title, availWidth-10)
		if len(name)+10 > availWidth {
			name = truncate(s.Title, availWidth-len(" - ")-6)
		}

		relTime := formatRelativeTime(s.UpdatedAt)
		line := bulletStyle.Render("• ") + labelStyle.Render(name) + valueStyle.Render(" - "+relTime)
		lines = append(lines, line)
	}

	result := ""
	for _, l := range lines {
		result += l + "\n"
	}

	if len(result) > 0 && result[len(result)-1] == '\n' {
		result = result[:len(result)-1]
	}

	return result
}

func formatRelativeTime(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	if d < 30*24*time.Hour {
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}
