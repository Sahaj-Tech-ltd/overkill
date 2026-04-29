package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/internal/personality"
)

type BootModel struct {
	soulMD          string
	funFact         string
	relationship    int
	personalityMode string
	visible         bool
	width           int
	height          int
	firstRun        bool
	person          *personality.Personality
}

func NewBootModel() BootModel {
	return BootModel{visible: true}
}

func LoadBootData(person *personality.Personality) tea.Cmd {
	return func() tea.Msg {
		msg := BootCompleteMsg{}

		soulPath := filepath.Join(os.Getenv("HOME"), ".ethos", "memories", "soul.md")
		data, err := os.ReadFile(soulPath)
		if err == nil {
			msg.SoulMD = string(data)
		}

		if person != nil {
			msg.FunFact = person.FunFacts().Random()
		}
		if msg.FunFact == "" {
			msg.FunFact = "73% of bugs are found between 2-4am."
		}

		return msg
	}
}

func (b *BootModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case BootCompleteMsg:
		b.soulMD = msg.SoulMD
		b.funFact = msg.FunFact
		b.visible = true
		return nil
	case tea.KeyMsg:
		if b.visible {
			b.visible = false
			return nil
		}
	}
	return nil
}

func (b BootModel) View() string {
	if !b.visible {
		return ""
	}

	logo := `
  ╔═══════════════════════════════╗
  ║       E T H O S               ║
  ║   The vibe-coding agent       ║
  ║   that has discipline.        ║
  ╚═══════════════════════════════╝`

	var parts []string
	parts = append(parts, logo)

	if b.funFact != "" {
		parts = append(parts, "")
		parts = append(parts, "Fun fact: "+b.funFact)
	}

	if b.firstRun {
		parts = append(parts, "")
		parts = append(parts, "hey you're finally awake")
	} else if b.soulMD != "" {
		excerpt := b.soulMD
		if len(excerpt) > 200 {
			excerpt = excerpt[:197] + "..."
		}
		parts = append(parts, "")
		parts = append(parts, excerpt)
	}

	parts = append(parts, "")
	parts = append(parts, "Press any key to continue...")

	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#cdd6f4")).
		Render(strings.Join(parts, "\n"))
}

func (b *BootModel) SetFirstRun(first bool) {
	b.firstRun = first
}
