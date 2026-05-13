package page

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type SetupCompleteMsg struct {
	Provider string
	Model    string
	Config   *config.Config
}

type spinMsg struct{}

type SetupPage struct {
	focus        int // 0=provider, 1=apiKey, 2=baseURL, 3=models
	providers    []string
	providerIdx  int
	apiKey       string
	baseURL      string
	models       []providers.Model
	cursor       int
	fetching     bool
	fetchErr     string
	saved        bool
	width        int
	height       int
	editing      bool
	editingField int // 1=apiKey, 2=baseURL
	providerOpen bool
	showPassword bool
	spinIdx      int
	spinner      spinner.Model
	cfg          *config.Config
}

var defaultBaseURLs = map[string]string{
	"openai":     "https://api.openai.com/v1",
	"anthropic":  "https://api.anthropic.com",
	"gemini":     "https://generativelanguage.googleapis.com/v1beta",
	"deepseek":   "https://api.deepseek.com/v1",
	"ollama":     "http://localhost:11434",
	"openrouter": "https://openrouter.ai/api/v1",
}

func NewSetupPage(cfg *config.Config) SetupPage {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return SetupPage{
		focus:        0,
		providers:    []string{"openai", "anthropic", "gemini", "deepseek", "ollama", "openrouter", "custom"},
		providerIdx:  0,
		baseURL:      defaultBaseURLs["openai"],
		models:       providers.OpenAIModels(),
		cursor:       0,
		spinner:      sp,
		editingField: -1,
		cfg:          cfg,
	}
}

func (m SetupPage) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchAfterDelay())
}

func (m SetupPage) Update(msg tea.Msg) (SetupPage, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case spinMsg:
		if m.fetching {
			m.spinIdx = (m.spinIdx + 1) % len(spinChars)
			return m, spinTick()
		}

	case providerFetchDoneMsg:
		m.fetching = false
		m.models = fetchModelsForProvider(m.providers[m.providerIdx])
		m.cursor = 0

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m SetupPage) handleKey(msg tea.KeyMsg) (SetupPage, tea.Cmd) {
	key := msg.String()

	if m.editing {
		return m.handleEditingKey(msg)
	}

	if m.providerOpen {
		return m.handleProviderDropdownKey(msg)
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyTab:
		m.focus = (m.focus + 1) % 4
		m.editing = false
		m.editingField = -1

	case tea.KeyRight:
		if m.focus < 3 {
			m.focus++
		} else {
			m.focus = 0
		}
		m.editing = false
		m.editingField = -1

	case tea.KeyLeft:
		if m.focus > 0 {
			m.focus--
		} else {
			m.focus = 3
		}
		m.editing = false
		m.editingField = -1

	case tea.KeyUp:
		if m.focus == 3 && len(m.models) > 0 && m.cursor > 0 {
			m.cursor--
		}

	case tea.KeyDown:
		if m.focus == 3 && len(m.models) > 0 && m.cursor < len(m.models)-1 {
			m.cursor++
		}

	case tea.KeyEnter:
		switch m.focus {
		case 0:
			m.providerOpen = true
		case 1:
			if m.providers[m.providerIdx] == "ollama" {
				break
			}
			m.editing = true
			m.editingField = 1
		case 2:
			m.editing = true
			m.editingField = 2
		case 3:
			if len(m.models) > 0 && m.cursor < len(m.models) {
				m.saved = true
				model := m.models[m.cursor]
				return m, emitComplete(m.providers[m.providerIdx], model.ID, m.cfg)
			}
		}

	case tea.KeyEsc:
		if m.editing {
			m.editing = false
			m.editingField = -1
		} else if m.focus != 0 {
			m.focus = 0
		}
	}

	if key == "c" && m.focus == 3 {
		if len(m.models) > 0 && m.cursor < len(m.models) {
			m.saved = true
			model := m.models[m.cursor]
			return m, emitComplete(m.providers[m.providerIdx], model.ID, m.cfg)
		}
	}

	return m, nil
}

func (m SetupPage) handleEditingKey(msg tea.KeyMsg) (SetupPage, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.editing = false
		m.editingField = -1

	case tea.KeyEsc:
		m.editing = false
		m.editingField = -1

	case tea.KeyBackspace:
		if m.editingField == 1 {
			if len(m.apiKey) > 0 {
				m.apiKey = m.apiKey[:len(m.apiKey)-1]
			}
		} else if m.editingField == 2 {
			if len(m.baseURL) > 0 {
				m.baseURL = m.baseURL[:len(m.baseURL)-1]
			}
		}

	case tea.KeyRunes:
		chars := string(msg.Runes)
		if m.editingField == 1 {
			m.apiKey += chars
		} else if m.editingField == 2 {
			m.baseURL += chars
		}
	}

	return m, nil
}

func (m SetupPage) handleProviderDropdownKey(msg tea.KeyMsg) (SetupPage, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.providerOpen = false
		m.onProviderChange()

	case tea.KeyEsc:
		m.providerOpen = false

	case tea.KeyUp:
		if m.providerIdx > 0 {
			m.providerIdx--
		}

	case tea.KeyDown:
		if m.providerIdx < len(m.providers)-1 {
			m.providerIdx++
		}
	}

	return m, nil
}

func (m *SetupPage) onProviderChange() {
	name := m.providers[m.providerIdx]
	if url, ok := defaultBaseURLs[name]; ok {
		m.baseURL = url
	}
	m.fetching = true
	m.models = nil
	m.cursor = 0
}

func (m SetupPage) View() string {
	if m.width <= 0 {
		return ""
	}

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#cba6f7")).
		PaddingBottom(0)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a6adc8")).
		PaddingBottom(1)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#89b4fa")).
		Width(14)

	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#cdd6f4"))

	focusedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f5c2e7")).
		Underline(true)

	disabledStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#585b70"))

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#a6e3a1")).
		Bold(true)

	cursorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#f9e2af"))

	passwordBullet := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#585b70"))

	var b strings.Builder

	// Title
	title := titleStyle.Render("Welcome to Ethos")
	subtitle := subtitleStyle.Render("Set up your first provider")

	b.WriteString(lipgloss.PlaceHorizontal(m.width, lipgloss.Center, title))
	b.WriteString("\n")
	b.WriteString(lipgloss.PlaceHorizontal(m.width, lipgloss.Center, subtitle))
	b.WriteString("\n\n")

	// Provider row
	providerLabel := renderLabel("Provider:", labelStyle, focusedStyle, m.focus == 0)
	providerName := m.providers[m.providerIdx]
	chevron := " ▾"
	if !m.providerOpen {
		chevron = " ▸"
	}
	providerValue := valueStyle.Render("[" + providerName + chevron + "]")
	b.WriteString(providerLabel)
	b.WriteString(providerValue)
	b.WriteString("\n")

	// Provider dropdown
	if m.providerOpen {
		for i, p := range m.providers {
			prefix := "     "
			if i == m.providerIdx {
				prefix = "  ▶  "
			}
			line := prefix + p
			styled := cursorStyle.Render(line)
			if i != m.providerIdx {
				styled = valueStyle.Render(line)
			}
			b.WriteString(lipgloss.PlaceHorizontal(m.width, lipgloss.Left, styled))
			b.WriteString("\n")
		}
	}

	// API Key row
	if m.providers[m.providerIdx] == "ollama" {
		apiLabelStyled := renderLabel("API Key:", labelStyle, focusedStyle, m.focus == 1)
		b.WriteString(apiLabelStyled)
		b.WriteString(disabledStyle.Render("[not required for Ollama]"))
		b.WriteString("\n")
	} else {
		apiLabelStyled := renderLabel("API Key:", labelStyle, focusedStyle, m.focus == 1)
		displayedKey := maskKey(m.apiKey, m.showPassword)
		if m.editing && m.editingField == 1 {
			displayedKey = m.apiKey + cursorChar
		}
		eyeIcon := "(•)"
		if m.showPassword {
			eyeIcon = "(⊙)"
		}
		keyValue := valueStyle.Render("[" + displayedKey + "]")
		eyeStyled := passwordBullet.Render(" " + eyeIcon)
		b.WriteString(apiLabelStyled)
		b.WriteString(keyValue)
		b.WriteString(eyeStyled)
		b.WriteString("\n")
	}

	// Base URL row
	baseLabel := renderLabel("Base URL:", labelStyle, focusedStyle, m.focus == 2)
	displayedURL := m.baseURL
	if m.editing && m.editingField == 2 {
		displayedURL = m.baseURL + cursorChar
	}
	baseValue := valueStyle.Render("[" + displayedURL + "]")
	b.WriteString(baseLabel)
	b.WriteString(baseValue)
	b.WriteString("\n\n")

	// Models section
	modelsHeader := "Models"
	if m.fetching {
		spinChar := spinChars[m.spinIdx%len(spinChars)]
		modelsHeader = fmt.Sprintf("%s %s", spinChar, "Fetching...")
	}
	if m.fetchErr != "" {
		modelsHeader = fmt.Sprintf("Error: %s", m.fetchErr)
	}

	headerStyled := labelStyle.Render(modelsHeader)
	b.WriteString(lipgloss.PlaceHorizontal(m.width, lipgloss.Left, headerStyled))
	b.WriteString("\n")

	if m.fetching {
		b.WriteString("  ")
		b.WriteString(valueStyle.Render("Loading models..."))
		b.WriteString("\n")
	} else if m.fetchErr != "" {
		b.WriteString("  ")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")).Render(m.fetchErr))
		b.WriteString("\n")
	} else {
		for i, model := range m.models {
			costStr := fmt.Sprintf("$%.2f / $%.2f per 1M tokens", model.CostIn, model.CostOut)
			bullet := "○"
			if i == m.cursor {
				bullet = "◉"
			}
			line := fmt.Sprintf("  %s %-20s %s", bullet, model.ID, costStr)

			styled := valueStyle.Render(line)
			if m.focus == 3 && i == m.cursor {
				styled = selectedStyle.Render(line)
			}
			b.WriteString(lipgloss.PlaceHorizontal(m.width, lipgloss.Left, styled))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")

	// Continue prompt
	continueText := "Press Enter on a model to continue"
	continueStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6c7086")).
		Render(continueText)
	b.WriteString(lipgloss.PlaceHorizontal(m.width, lipgloss.Center, continueStyled))

	return b.String()
}

func renderLabel(text string, labelStyle, focusedStyle lipgloss.Style, focused bool) string {
	if focused {
		return focusedStyle.Render(text)
	}
	return labelStyle.Render(text)
}

func maskKey(key string, show bool) string {
	if show {
		return key
	}
	if len(key) == 0 {
		return ""
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	return strings.Repeat("*", len(key)-4) + key[len(key)-4:]
}

func emitComplete(provider, model string, cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		return SetupCompleteMsg{Provider: provider, Model: model, Config: cfg}
	}
}

func fetchModelsForProvider(name string) []providers.Model {
	switch name {
	case "openai":
		return providers.OpenAIModels()
	case "anthropic":
		return providers.AnthropicModels()
	case "gemini":
		return providers.GeminiModels()
	case "deepseek":
		return providers.DeepSeekModels()
	case "ollama":
		return providers.OllamaModels()
	case "openrouter":
		return providers.OpenRouterModels()
	default:
		return providers.OpenAIModels()
	}
}

type providerFetchDoneMsg struct{}

func fetchAfterDelay() tea.Cmd {
	return tea.Tick(600*time.Millisecond, func(t time.Time) tea.Msg {
		return providerFetchDoneMsg{}
	})
}

func spinTick() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return spinMsg{}
	})
}

var spinChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
var cursorChar = "▌"

func (m SetupPage) SelectedProvider() string {
	return m.providers[m.providerIdx]
}

func (m SetupPage) SelectedModel() string {
	if m.cursor < len(m.models) {
		return m.models[m.cursor].ID
	}
	return ""
}

func (m SetupPage) APIKey() string {
	return m.apiKey
}

func (m SetupPage) BaseURL() string {
	return m.baseURL
}

func (m SetupPage) SaveConfig(cfg *config.Config) error {
	provider := m.providers[m.providerIdx]
	model := ""
	if m.cursor < len(m.models) {
		model = m.models[m.cursor].ID
	}

	cfg.Agent.DefaultProvider = provider
	cfg.Agent.DefaultModel = model

	found := false
	for i, p := range cfg.Providers {
		if p.Name == provider {
			cfg.Providers[i].APIKey = m.apiKey
			cfg.Providers[i].BaseURL = m.baseURL
			found = true
			break
		}
	}
	if !found {
		cfg.Providers = append(cfg.Providers, config.ProviderConfig{
			Name:    provider,
			Type:    provider,
			APIKey:  m.apiKey,
			BaseURL: m.baseURL,
		})
	}

	path, err := config.ConfigPath()
	if err != nil {
		return fmt.Errorf("setup: getting config path: %w", err)
	}
	return cfg.Save(path)
}

func (m SetupPage) Done() bool {
	return m.saved
}
