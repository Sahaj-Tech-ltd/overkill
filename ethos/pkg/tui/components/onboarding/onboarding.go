// Package onboarding — first-run guided wizard.
//
// Shown once per fresh install (gated by ~/.overkill/onboarded). Walks the user
// through model selection, API-key entry, optional features, and a quick
// reference card. Esc skips, recording the skip in the marker file so we
// don't pester them again.
package onboarding

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// CompleteMsg is emitted when the wizard finishes (either by completion or
// skip). The TUI parent listens for this to swap onboarding off and the
// chat page on.
type CompleteMsg struct {
	Skipped bool
	Config  *config.Config
}

// Step indexes the wizard's screens.
type Step int

const (
	StepWelcome Step = iota
	StepModel
	StepAPIKey
	StepOptional
	StepDone
)

// Model is the onboarding state machine.
type Model struct {
	Width  int
	Height int

	step    Step
	cfg     *config.Config
	homeDir string

	// Step 2 — Pick a model.
	providerCursor int
	providers      []providerOption

	// Step 3 — API key.
	apiKey    string
	apiCursor int // 0 = key field, 1 = back, 2 = next

	// Step 4 — Optional features (3 checkboxes).
	optCursor     int
	enableSync    bool
	enableACP     bool
	enablePlugins bool
}

type providerOption struct {
	Name  string
	Label string
	Model string // suggested default model id
}

// New returns an onboarding wizard seeded from cfg. cfg is mutated in place
// as the user advances through the steps; the caller should persist it on
// CompleteMsg if they want the choices to stick.
func New(cfg *config.Config) Model {
	if cfg == nil {
		cfg = &config.Config{Version: config.CurrentVersion}
	}
	home, _ := os.UserHomeDir()
	return Model{
		cfg:     cfg,
		homeDir: home,
		providers: []providerOption{
			{Name: "anthropic", Label: "Anthropic Claude", Model: "claude-3-5-sonnet-20241022"},
			{Name: "openai", Label: "OpenAI GPT-4", Model: "gpt-4o"},
			{Name: "gemini", Label: "Google Gemini", Model: "gemini-2.0-flash"},
			{Name: "ollama", Label: "Ollama (local)", Model: "llama3.2"},
		},
	}
}

// SetSize wires terminal dimensions so the onboarding View can centre.
func (m *Model) SetSize(w, h int) {
	m.Width = w
	m.Height = h
}

// Step returns the current wizard step. Used by tests.
func (m Model) Step() Step { return m.step }

// IsDone reports whether the wizard has emitted (or is about to emit)
// CompleteMsg.
func (m Model) IsDone() bool { return m.step > StepDone }

// Init returns no commands — the wizard is fully event-driven.
func (m Model) Init() tea.Cmd { return nil }

// Update handles every key the parent forwards. Esc on any step skips the
// remaining wizard. Enter advances; arrows move within the current step.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if k.String() == "esc" {
		return m, m.skip()
	}
	switch m.step {
	case StepWelcome:
		return m.updateWelcome(k)
	case StepModel:
		return m.updateModel(k)
	case StepAPIKey:
		return m.updateAPIKey(k)
	case StepOptional:
		return m.updateOptional(k)
	case StepDone:
		return m.updateDone(k)
	}
	return m, nil
}

func (m Model) updateWelcome(k tea.KeyMsg) (Model, tea.Cmd) {
	switch k.String() {
	case "enter", "right", "n":
		m.step = StepModel
	}
	return m, nil
}

func (m Model) updateModel(k tea.KeyMsg) (Model, tea.Cmd) {
	switch k.String() {
	case "up":
		if m.providerCursor > 0 {
			m.providerCursor--
		}
	case "down":
		if m.providerCursor < len(m.providers)-1 {
			m.providerCursor++
		}
	case "left", "b":
		m.step = StepWelcome
	case "enter", "right", "n":
		choice := m.providers[m.providerCursor]
		m.cfg.Agent.DefaultProvider = choice.Name
		m.cfg.Agent.DefaultModel = choice.Model
		m.step = StepAPIKey
	}
	return m, nil
}

func (m Model) updateAPIKey(k tea.KeyMsg) (Model, tea.Cmd) {
	switch k.String() {
	case "up":
		if m.apiCursor > 0 {
			m.apiCursor--
		}
	case "down":
		if m.apiCursor < 2 {
			m.apiCursor++
		}
	case "tab":
		m.apiCursor = (m.apiCursor + 1) % 3
	case "left":
		if m.apiCursor == 1 {
			m.step = StepModel
		}
	case "right":
		if m.apiCursor == 2 {
			m.advanceFromAPIKey()
		}
	case "enter":
		// Enter on the key field also advances; otherwise honor the focused button.
		switch m.apiCursor {
		case 0, 2:
			m.advanceFromAPIKey()
		case 1:
			m.step = StepModel
		}
	case "backspace":
		if m.apiCursor == 0 && len(m.apiKey) > 0 {
			m.apiKey = m.apiKey[:len(m.apiKey)-1]
		}
	default:
		if m.apiCursor == 0 && k.Type == tea.KeyRunes && len(k.Runes) > 0 {
			m.apiKey += string(k.Runes)
		}
	}
	return m, nil
}

// advanceFromAPIKey commits the typed key into the config's provider list
// and moves to the optional features step.
func (m *Model) advanceFromAPIKey() {
	if strings.TrimSpace(m.apiKey) != "" {
		// Upsert into Providers slice.
		found := false
		for i, p := range m.cfg.Providers {
			if p.Name == m.cfg.Agent.DefaultProvider {
				m.cfg.Providers[i].APIKey = m.apiKey
				found = true
				break
			}
		}
		if !found {
			m.cfg.Providers = append(m.cfg.Providers, config.ProviderConfig{
				Name:   m.cfg.Agent.DefaultProvider,
				Type:   m.cfg.Agent.DefaultProvider,
				APIKey: m.apiKey,
			})
		}
	}
	m.step = StepOptional
}

func (m Model) updateOptional(k tea.KeyMsg) (Model, tea.Cmd) {
	switch k.String() {
	case "up":
		if m.optCursor > 0 {
			m.optCursor--
		}
	case "down":
		if m.optCursor < 3 { // 0..2 = checkboxes, 3 = next
			m.optCursor++
		}
	case " ", "space":
		switch m.optCursor {
		case 0:
			m.enableSync = !m.enableSync
		case 1:
			m.enableACP = !m.enableACP
		case 2:
			m.enablePlugins = !m.enablePlugins
		}
	case "left", "b":
		m.step = StepAPIKey
	case "enter", "right", "n":
		if m.optCursor < 3 {
			// Toggle on space/enter for checkboxes too — but only enter on
			// the "next" row advances.
			switch m.optCursor {
			case 0:
				m.enableSync = !m.enableSync
			case 1:
				m.enableACP = !m.enableACP
			case 2:
				m.enablePlugins = !m.enablePlugins
			}
			return m, nil
		}
		// Apply the optional feature choices.
		if m.enableSync {
			m.cfg.Sync.Backend = "file"
		}
		if m.enableACP {
			m.cfg.ACP.Enabled = true
		}
		if !m.enablePlugins {
			// Plugins are on by default; only mute by clearing the dir.
			m.cfg.Plugins.Dir = "/dev/null/disabled"
		}
		m.step = StepDone
	}
	return m, nil
}

func (m Model) updateDone(_ tea.KeyMsg) (Model, tea.Cmd) {
	return m, m.complete()
}

func (m Model) skip() tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		writeMarker(m.homeDir, true)
		return CompleteMsg{Skipped: true, Config: cfg}
	}
}

func (m Model) complete() tea.Cmd {
	cfg := m.cfg
	home := m.homeDir
	return func() tea.Msg {
		writeMarker(home, false)
		// Best-effort: persist the config so onboarded choices stick.
		if path, err := config.ConfigPath(); err == nil {
			_ = cfg.Save(path)
		}
		return CompleteMsg{Skipped: false, Config: cfg}
	}
}

// MarkerPath returns the file used to record completion of the onboarding
// wizard. Exported so the parent and tests share the same source of truth.
func MarkerPath(homeDir string) string {
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	return filepath.Join(homeDir, ".overkill", "onboarded")
}

// HasOnboarded returns true when the marker file exists.
func HasOnboarded(homeDir string) bool {
	_, err := os.Stat(MarkerPath(homeDir))
	return err == nil
}

// markerPayload is the JSON written into the marker file. Lets future versions
// distinguish a clean completion from a skip and ratchet on schema bumps.
type markerPayload struct {
	Version     int       `json:"version"`
	Skipped     bool      `json:"skipped"`
	CompletedAt time.Time `json:"completed_at"`
}

func writeMarker(homeDir string, skipped bool) {
	path := MarkerPath(homeDir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	body, _ := json.Marshal(markerPayload{
		Version:     1,
		Skipped:     skipped,
		CompletedAt: time.Now().UTC(),
	})
	_ = os.WriteFile(path, body, 0o600)
}

// View renders the current step centered on the screen.
func (m Model) View() string {
	t := theme.CurrentTheme()
	titleStyle := lipgloss.NewStyle().Foreground(t.Accent()).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(t.TextMuted())
	cursorStyle := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Accent()).Bold(true)
	rowStyle := lipgloss.NewStyle().Foreground(t.Text())
	hintStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Italic(true)

	var body string
	switch m.step {
	case StepWelcome:
		logo := titleStyle.Render(`OVERKILL`)
		tagline := lipgloss.NewStyle().
			Foreground(t.Secondary()).
			Italic(true).
			Render("the vibe-coding agent")
		pitch := rowStyle.Render(
			"a terminal-native AI coding partner with multi-provider support,\n" +
				"sub-agents, MCP/LSP integration, and plugin extensibility.",
		)
		body = strings.Join([]string{
			logo, tagline, "", pitch, "",
			hintStyle.Render("press enter to begin · esc to skip"),
		}, "\n")
	case StepModel:
		var rows []string
		rows = append(rows, titleStyle.Render("Pick a model"))
		rows = append(rows, mutedStyle.Render("you can switch later from /model or ctrl+o"))
		rows = append(rows, "")
		for i, p := range m.providers {
			line := fmt.Sprintf("  %s  %s", p.Label, mutedStyle.Render("("+p.Model+")"))
			if i == m.providerCursor {
				rows = append(rows, cursorStyle.Render(line))
			} else {
				rows = append(rows, rowStyle.Render(line))
			}
		}
		rows = append(rows, "", hintStyle.Render("↑/↓ select · enter next · esc skip"))
		body = strings.Join(rows, "\n")
	case StepAPIKey:
		var rows []string
		rows = append(rows, titleStyle.Render("Let's get you connected"))
		rows = append(rows,
			mutedStyle.Render("paste your API key for "+m.cfg.Agent.DefaultProvider))
		rows = append(rows, "")
		mask := strings.Repeat("•", len(m.apiKey))
		field := fmt.Sprintf("  key: %s_", mask)
		if m.apiCursor == 0 {
			rows = append(rows, cursorStyle.Render(field))
		} else {
			rows = append(rows, rowStyle.Render(field))
		}
		rows = append(rows, "")
		back := "  [ back ]"
		next := "  [ next ]"
		if m.apiCursor == 1 {
			back = cursorStyle.Render(back)
		}
		if m.apiCursor == 2 {
			next = cursorStyle.Render(next)
		}
		rows = append(rows, back+"   "+next)
		rows = append(rows, "", hintStyle.Render("type to enter key · tab/↑↓ to switch · enter to advance · esc skip"))
		body = strings.Join(rows, "\n")
	case StepOptional:
		var rows []string
		rows = append(rows, titleStyle.Render("Optional features"))
		rows = append(rows, mutedStyle.Render("toggle now or change later in /config"))
		rows = append(rows, "")
		opts := []struct {
			label string
			desc  string
			on    bool
		}{
			{"sync", "share sessions across machines", m.enableSync},
			{"acp", "expose this agent to other agents over HTTP", m.enableACP},
			{"plugins", "load custom plugins from ~/.overkill/plugins", m.enablePlugins},
		}
		for i, o := range opts {
			mark := "[ ]"
			if o.on {
				mark = "[x]"
			}
			line := fmt.Sprintf("  %s %-9s %s", mark, o.label, mutedStyle.Render(o.desc))
			if i == m.optCursor {
				rows = append(rows, cursorStyle.Render(line))
			} else {
				rows = append(rows, rowStyle.Render(line))
			}
		}
		rows = append(rows, "")
		next := "  [ all set → ]"
		if m.optCursor == 3 {
			next = cursorStyle.Render(next)
		}
		rows = append(rows, next)
		rows = append(rows, "", hintStyle.Render("↑/↓ move · space toggle · enter on 'all set' to finish · esc skip"))
		body = strings.Join(rows, "\n")
	case StepDone:
		body = strings.Join([]string{
			titleStyle.Render("All set."),
			"",
			rowStyle.Render("  /help          show every command + keybinding"),
			rowStyle.Render("  ctrl+k        command palette"),
			rowStyle.Render("  ctrl+,  / f2  config"),
			rowStyle.Render("  @<path>       mention a file in your prompt"),
			"",
			hintStyle.Render("press any key to start chatting"),
		}, "\n")
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Accent()).
		Padding(1, 3).
		Render(body)

	if m.Width <= 0 || m.Height <= 0 {
		return box
	}
	return lipgloss.Place(m.Width, m.Height,
		lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
	)
}
