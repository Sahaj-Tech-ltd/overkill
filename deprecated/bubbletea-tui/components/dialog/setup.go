package dialog

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
	"github.com/Sahaj-Tech-ltd/overkill/internal/auth"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// oauthSupportedProviders is the set the wizard can drive through device
// flow. Mirror of internal/auth.providerConfigs keys. Anything outside this
// set falls back to manual API key entry.
var oauthSupportedProviders = map[string]bool{
	"anthropic": true,
	"copilot":   true,
}

// SetupOAuthStartedMsg signals the dialog kicked off a device flow. The TUI
// uses this to show the user code/URL prominently.
type SetupOAuthStartedMsg struct {
	Provider        string
	UserCode        string
	VerificationURL string
}

// SetupOAuthCompleteMsg fires when polling returns a token. The TUI maps it
// to a SetupSavedMsg using the provider/model defaults already chosen.
type SetupOAuthCompleteMsg struct {
	Provider string
	Token    string
	Err      error
}

// oauthFlow is the minimal shape the dialog needs from a device flow.
// Tests stub via oauthStart/oauthPoll.
type oauthFlow struct {
	userCode string
	verURL   string
	flow     *auth.DeviceFlow
}

// oauthStart and oauthPoll are split so the dialog can render the user
// code immediately while a background poll waits for authorization.
var oauthStart = func(ctx context.Context, provider string) (*oauthFlow, error) {
	f, err := auth.StartDeviceFlow(ctx, provider)
	if err != nil {
		return nil, err
	}
	return &oauthFlow{userCode: f.UserCode, verURL: f.VerificationURL, flow: f}, nil
}

var oauthPoll = func(ctx context.Context, of *oauthFlow) (string, error) {
	if of == nil || of.flow == nil {
		return "", fmt.Errorf("auth: nil flow")
	}
	tok, err := auth.PollForToken(ctx, of.flow)
	if err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

const maxSetupWidth = 52

type ShowSetupDialogMsg struct{}
type CloseSetupDialogMsg struct{}
type SetupSavedMsg struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
}

type SetupDialog struct {
	Dialog
	providers   []string
	providerIdx int
	apiKey      string
	editingKey  bool
	models      []providers.Model
	modelIdx    int
	step        int
	saved       bool
	fetchErr    string

	// OAuth (device-flow) sub-state. Populated when the user picks (b) on
	// the API-key step for an oauth-supported provider.
	oauthActive          bool
	oauthUserCode        string
	oauthVerificationURL string
	oauthErr             string
	oauthCancel          context.CancelFunc
}

func NewSetupDialog() SetupDialog {
	provs := []string{"openai", "anthropic", "gemini", "deepseek", "ollama", "openrouter", "custom"}
	return SetupDialog{
		Dialog:    Dialog{Title: "Configure Provider"},
		providers: provs,
		step:      0,
	}
}

func (m SetupDialog) Init() tea.Cmd {
	return nil
}

func (m SetupDialog) Update(msg tea.Msg) (SetupDialog, tea.Cmd) {
	switch msg := msg.(type) {
	case ShowSetupDialogMsg:
		m.Show = true
		m.step = 0
		m.providerIdx = 0
		m.apiKey = ""
		m.editingKey = false
		m.modelIdx = 0
		m.models = nil
		m.saved = false
		m.fetchErr = ""
		m.cancelOAuth()
		return m, nil
	case setupOAuthStartedInternal:
		m.oauthUserCode = msg.started.UserCode
		m.oauthVerificationURL = msg.started.VerificationURL
		// Forward the user-facing Started msg AND chain the poll command.
		return m, tea.Batch(
			func() tea.Msg { return msg.started },
			msg.poll,
		)
	case SetupOAuthCompleteMsg:
		m.oauthActive = false
		if msg.Err != nil {
			m.oauthErr = msg.Err.Error()
			return m, nil
		}
		m.apiKey = msg.Token
		m.editingKey = false
		m.step = 2
		m.loadModelsForProvider(m.providers[m.providerIdx])
		return m, nil
	case CloseSetupDialogMsg:
		m.Show = false
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m SetupDialog) handleKey(k tea.KeyMsg) (SetupDialog, tea.Cmd) {
	ks := k.String()

	switch {
	case ks == "esc":
		// While an OAuth poll is in flight, esc cancels the poll and stays
		// on the key step. Don't decrement step.
		if m.oauthActive {
			m.cancelOAuth()
			return m, nil
		}
		if m.step == 0 {
			m.Show = false
			return m, func() tea.Msg { return CloseSetupDialogMsg{} }
		}
		m.step--
		m.editingKey = false
		return m, nil

	case ks == "tab":
		if m.editingKey {
			return m, nil
		}
		switch m.step {
		case 0:
			m.selectProviderAndNext()
			return m, nil
		case 2:
			return m, nil
		}
		return m, nil
	}

	switch m.step {
	case 0:
		return m.handleProviderStep(k)
	case 1:
		return m.handleKeyStep(k)
	case 2:
		return m.handleModelStep(k)
	}
	return m, nil
}

func (m *SetupDialog) selectProviderAndNext() {
	prov := m.providers[m.providerIdx]
	m.step = 1
	m.apiKey = ""
	m.editingKey = false
	if prov == "ollama" {
		m.step = 2
		m.loadModelsForProvider(prov)
	}
}

func (m SetupDialog) handleProviderStep(k tea.KeyMsg) (SetupDialog, tea.Cmd) {
	switch k.String() {
	case "up":
		if m.providerIdx > 0 {
			m.providerIdx--
		} else {
			m.providerIdx = len(m.providers) - 1
		}
	case "down":
		if m.providerIdx < len(m.providers)-1 {
			m.providerIdx++
		} else {
			m.providerIdx = 0
		}
	case "enter":
		m.selectProviderAndNext()
		return m, nil
	}
	return m, nil
}

func (m SetupDialog) handleKeyStep(k tea.KeyMsg) (SetupDialog, tea.Cmd) {
	prov := m.providers[m.providerIdx]
	if prov == "ollama" {
		m.step = 2
		m.loadModelsForProvider(prov)
		return m, nil
	}

	// OAuth in flight: only Esc is meaningful — it cancels the poll and
	// drops us back into the manual key-entry step.
	if m.oauthActive {
		if k.String() == "esc" {
			m.cancelOAuth()
			return m, nil
		}
		return m, nil
	}

	// Browser sign-in branch (only for providers we have device flow for).
	if !m.editingKey && k.String() == "b" && oauthSupportedProviders[prov] {
		return m.startOAuth(prov)
	}

	if m.editingKey {
		switch k.String() {
		case "enter":
			m.editingKey = false
			m.step = 2
			m.loadModelsForProvider(prov)
			return m, nil
		case "backspace":
			if len(m.apiKey) > 0 {
				lastRune := m.apiKey[len(m.apiKey)-1]
				if lastRune < 128 {
					m.apiKey = m.apiKey[:len(m.apiKey)-1]
				} else {
					m.apiKey = m.apiKey[:len(m.apiKey)-1]
					for len(m.apiKey) > 0 && m.apiKey[len(m.apiKey)-1]&0xC0 == 0x80 {
						m.apiKey = m.apiKey[:len(m.apiKey)-1]
					}
				}
			}
		default:
			if len(k.Runes) > 0 {
				m.apiKey += string(k.Runes)
			}
		}
	} else {
		switch k.String() {
		case "enter":
			m.editingKey = true
			m.apiKey = ""
		}
	}
	return m, nil
}

func (m SetupDialog) handleModelStep(k tea.KeyMsg) (SetupDialog, tea.Cmd) {
	switch k.String() {
	case "up":
		if m.modelIdx > 0 {
			m.modelIdx--
		} else if len(m.models) > 0 {
			m.modelIdx = len(m.models) - 1
		}
	case "down":
		if m.modelIdx < len(m.models)-1 {
			m.modelIdx++
		} else {
			m.modelIdx = 0
		}
	case "enter":
		if len(m.models) > 0 {
			return m, func() tea.Msg {
				return m.buildSavedMsg()
			}
		}
	}
	return m, nil
}

func (m SetupDialog) buildSavedMsg() SetupSavedMsg {
	prov := m.providers[m.providerIdx]
	mdl := m.models[m.modelIdx]
	baseURL := providerBaseURL(prov)
	return SetupSavedMsg{
		Provider: prov,
		Model:    mdl.ID,
		APIKey:   m.apiKey,
		BaseURL:  baseURL,
	}
}

// startOAuth kicks off the device flow. Returns a tea.Cmd that
// synchronously calls the device endpoint (fast) to obtain the user code
// and URL, surfaces them via SetupOAuthStartedMsg, and chains a follow-up
// cmd that long-polls for the access token.
func (m SetupDialog) startOAuth(provider string) (SetupDialog, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	m.oauthActive = true
	m.oauthErr = ""
	m.oauthCancel = cancel

	startCmd := func() tea.Msg {
		of, err := oauthStart(ctx, provider)
		if err != nil {
			return SetupOAuthCompleteMsg{Provider: provider, Err: err}
		}
		return setupOAuthStartedInternal{
			provider: provider,
			started:  SetupOAuthStartedMsg{Provider: provider, UserCode: of.userCode, VerificationURL: of.verURL},
			poll: func() tea.Msg {
				token, err := oauthPoll(ctx, of)
				if err != nil {
					return SetupOAuthCompleteMsg{Provider: provider, Err: err}
				}
				return SetupOAuthCompleteMsg{Provider: provider, Token: token}
			},
		}
	}
	return m, startCmd
}

// setupOAuthStartedInternal is the dialog's chained envelope: it carries
// the user-visible Started message AND the follow-up poll cmd. The host
// unwraps and forwards both to bubbletea.
type setupOAuthStartedInternal struct {
	provider string
	started  SetupOAuthStartedMsg
	poll     tea.Cmd
}

func (m *SetupDialog) cancelOAuth() {
	if m.oauthCancel != nil {
		m.oauthCancel()
		m.oauthCancel = nil
	}
	m.oauthActive = false
	m.oauthUserCode = ""
	m.oauthVerificationURL = ""
}

// HandleOAuthStarted is called by the host when SetupOAuthStartedMsg fires
// so the dialog can show the user code/URL prominently.
func (m *SetupDialog) HandleOAuthStarted(msg SetupOAuthStartedMsg) {
	m.oauthUserCode = msg.UserCode
	m.oauthVerificationURL = msg.VerificationURL
}

func (m *SetupDialog) loadModelsForProvider(prov string) {
	if prov == "custom" {
		m.models = nil
		m.fetchErr = "no preset catalog for custom provider"
		return
	}
	dir := filepath.Join("models", providerDir(prov))
	cat, err := providers.LoadCatalog(dir)
	if err != nil {
		m.models = nil
		m.fetchErr = fmt.Sprintf("failed to load models: %v", err)
		return
	}
	ids := cat.List()
	m.models = make([]providers.Model, 0, len(ids))
	for _, id := range ids {
		mdl, err := cat.Get(id)
		if err != nil {
			continue
		}
		m.models = append(m.models, *mdl)
	}
	m.modelIdx = 0
	if len(m.models) == 0 {
		m.fetchErr = "no models found"
	}
}

func providerDir(provider string) string {
	m := map[string]string{
		"openai":     "openai",
		"anthropic":  "anthropic",
		"gemini":     "google",
		"deepseek":   "deepseek",
		"ollama":     "ollama",
		"openrouter": "openrouter",
	}
	if d, ok := m[provider]; ok {
		return d
	}
	return provider
}

func providerBaseURL(provider string) string {
	urls := map[string]string{
		"openai":     "https://api.openai.com/v1",
		"anthropic":  "https://api.anthropic.com",
		"gemini":     "https://generativelanguage.googleapis.com/v1beta",
		"deepseek":   "https://api.deepseek.com/v1",
		"ollama":     "http://localhost:11434",
		"openrouter": "https://openrouter.ai/api/v1",
		"custom":     "",
	}
	return urls[provider]
}

func (m SetupDialog) View(totalWidth, totalHeight int) string {
	if !m.Show {
		return ""
	}

	t := theme.CurrentTheme()
	baseStyle := lipgloss.NewStyle()

	title := baseStyle.
		Foreground(t.Primary()).
		Bold(true).
		Width(maxSetupWidth).
		Padding(0, 1, 1).
		Render("Configure Provider")

	var body string
	switch m.step {
	case 0:
		body = m.viewProviderStep(t, baseStyle)
	case 1:
		body = m.viewKeyStep(t, baseStyle)
	case 2:
		body = m.viewModelStep(t, baseStyle)
	}

	footer := m.viewFooter(t, baseStyle)

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		body,
		footer,
	)

	return baseStyle.Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderBackground(t.Background()).
		BorderForeground(t.TextMuted()).
		Width(lipgloss.Width(content) + 4).
		Render(content)
}

func (m SetupDialog) viewProviderStep(t theme.Theme, base lipgloss.Style) string {
	var items []string
	for i, p := range m.providers {
		style := base.Width(maxSetupWidth)
		if i == m.providerIdx {
			style = style.Background(t.Primary()).
				Foreground(t.Background()).
				Bold(true)
		} else {
			style = style.Foreground(t.Text())
		}
		items = append(items, style.Render("  "+p))
	}
	return base.Width(maxSetupWidth).
		Render(lipgloss.JoinVertical(lipgloss.Left, items...))
}

func (m SetupDialog) viewKeyStep(t theme.Theme, base lipgloss.Style) string {
	prov := m.providers[m.providerIdx]
	if prov == "ollama" {
		return base.Width(maxSetupWidth).
			Foreground(t.TextMuted()).
			Padding(1, 1).
			Render("Ollama runs locally — no API key needed.  Press Enter or Tab to continue.")
	}

	if m.oauthActive {
		title := base.Foreground(t.Primary()).Bold(true).
			Width(maxSetupWidth).Padding(0, 1).
			Render("Sign in with browser")
		body := "Waiting for device-flow authorization..."
		if m.oauthUserCode != "" {
			body = fmt.Sprintf("Open  %s\nEnter code:  %s",
				m.oauthVerificationURL, m.oauthUserCode)
		}
		bodyR := base.Foreground(t.Text()).Width(maxSetupWidth).Padding(1, 1).Render(body)
		hint := base.Foreground(t.TextMuted()).Width(maxSetupWidth).Padding(0, 1).
			Render("esc: cancel and return to API key entry")
		return base.Width(maxSetupWidth).
			Render(lipgloss.JoinVertical(lipgloss.Left, title, bodyR, hint))
	}

	labelStyle := base.Foreground(t.Primary()).Width(maxSetupWidth).Padding(0, 1)
	label := labelStyle.Render("API Key")

	var keyDisplay string
	if m.editingKey {
		if m.apiKey == "" {
			keyDisplay = "_"
		} else {
			keyDisplay = MaskKey(m.apiKey, 5)
		}
	} else {
		if m.apiKey != "" {
			keyDisplay = MaskKey(m.apiKey, 5)
		} else {
			keyDisplay = "(press Enter to edit)"
		}
	}

	displayStyle := base.
		Foreground(t.Text()).
		Width(maxSetupWidth).
		Padding(0, 1)

	displayText := displayStyle.Render(keyDisplay)

	hint := base.
		Foreground(t.TextMuted()).
		Width(maxSetupWidth).
		Padding(0, 1).
		Render("Enter to edit | Esc to go back")

	return base.Width(maxSetupWidth).
		Render(lipgloss.JoinVertical(lipgloss.Left, label, displayText, base.Render(""), hint))
}

func (m SetupDialog) viewModelStep(t theme.Theme, base lipgloss.Style) string {
	if m.fetchErr != "" {
		return base.Width(maxSetupWidth).
			Foreground(t.Error()).
			Padding(1, 1).
			Render(m.fetchErr)
	}
	if len(m.models) == 0 {
		return base.Width(maxSetupWidth).
			Foreground(t.TextMuted()).
			Padding(1, 1).
			Render("Loading models...")
	}

	var items []string
	start := 0
	end := len(m.models)
	if end > 10 {
		if m.modelIdx >= 5 {
			start = m.modelIdx - 4
			end = start + 10
			if end > len(m.models) {
				end = len(m.models)
				start = max(0, end-10)
			}
		} else {
			end = 10
		}
	}

	for i := start; i < end; i++ {
		mdl := m.models[i]
		entry := fmt.Sprintf("%s  ($%.2f/$%.2f)", mdl.Name, mdl.CostIn, mdl.CostOut)
		if len(entry) > maxSetupWidth-4 {
			entry = entry[:maxSetupWidth-7] + "..."
		}
		style := base.Width(maxSetupWidth)
		if i == m.modelIdx {
			style = style.Background(t.Primary()).
				Foreground(t.Background()).
				Bold(true)
		} else {
			style = style.Foreground(t.Text())
		}
		items = append(items, style.Render("  "+entry))
	}

	return base.Width(maxSetupWidth).
		Render(lipgloss.JoinVertical(lipgloss.Left, items...))
}

func (m SetupDialog) viewFooter(t theme.Theme, base lipgloss.Style) string {
	var parts []string
	switch m.step {
	case 0:
		parts = []string{"↑/↓ select", "enter confirm", "esc cancel"}
	case 1:
		parts = []string{"enter edit/confirm", "esc back"}
		if oauthSupportedProviders[m.providers[m.providerIdx]] {
			parts = append(parts, "b sign in with browser")
		}
	case 2:
		if len(m.models) > 0 {
			parts = []string{"↑/↓ select", "enter confirm", "esc back"}
		} else {
			parts = []string{"esc back"}
		}
	}
	helper := strings.Join(parts, "  │  ")
	return base.
		Foreground(t.TextMuted()).
		Width(maxSetupWidth).
		Padding(1, 1, 0).
		Align(lipgloss.Center).
		Render(helper)
}

func SaveToConfig(msg SetupSavedMsg) error {
	cfgPath, err := config.ConfigPath()
	if err != nil {
		return fmt.Errorf("setup: config path: %w", err)
	}
	cfg := config.Default()
	cfg.Agent.DefaultProvider = msg.Provider
	cfg.Agent.DefaultModel = msg.Model
	cfg.Providers = []config.ProviderConfig{
		{
			Name:    msg.Provider,
			Type:    msg.Provider,
			APIKey:  msg.APIKey,
			BaseURL: msg.BaseURL,
			Models: []config.ModelConfig{
				{ID: msg.Model, Name: msg.Model},
			},
		},
	}
	return cfg.Save(cfgPath)
}
