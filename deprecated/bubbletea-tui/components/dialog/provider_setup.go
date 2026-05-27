// Package dialog — provider_setup.go is a focused 3-step wizard opened when
// the user picks a model from /model whose provider has no API key
// configured. Steps:
//
//  1. API key entry (masked, last-5 visible while typing)
//  2. Endpoint selection (default from catalog, Tab toggles custom input)
//  3. Save & switch (emits ProviderConfiguredMsg)
//
// Distinct from SetupDialog (the first-run multi-screen flow) so we can
// dispatch directly into a known provider/model without re-prompting the user
// to pick those again.
package dialog

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
)

// ProviderConfiguredMsg is emitted when the user completes the wizard.
// The TUI persists this to config and reconfigures the agent.
type ProviderConfiguredMsg struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
}

// CloseProviderSetupMsg dismisses the dialog without saving.
type CloseProviderSetupMsg struct{}

// ProviderSetupDialog is a tiny state machine: 0=key, 1=endpoint, 2=confirm.
type ProviderSetupDialog struct {
	Dialog
	Provider       string
	Model          string
	DefaultBaseURL string

	step      int
	apiKey    string
	useCustom bool
	customURL string
}

// NewProviderSetupDialog returns a hidden dialog. Open() seeds it.
func NewProviderSetupDialog() ProviderSetupDialog {
	return ProviderSetupDialog{Dialog: Dialog{Title: "Configure Provider"}}
}

// Open seeds the dialog with a provider/model/default-url and shows it.
func (p *ProviderSetupDialog) Open(provider, model, defaultURL string) {
	p.OpenWithExisting(provider, model, defaultURL, "", "")
}

// OpenWithExisting pre-fills the wizard with the user's current api key and
// base URL so they only edit what's changing. Used when the user picks a
// model whose provider is already configured and elects to update credentials.
func (p *ProviderSetupDialog) OpenWithExisting(provider, model, defaultURL, existingKey, existingURL string) {
	p.Provider = provider
	p.Model = model
	p.DefaultBaseURL = defaultURL
	p.step = 0
	p.apiKey = existingKey
	p.useCustom = existingURL != "" && existingURL != defaultURL
	p.customURL = existingURL
	p.Show = true
}

func (p ProviderSetupDialog) Update(msg tea.Msg) (ProviderSetupDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "esc":
		p.Show = false
		return p, func() tea.Msg { return CloseProviderSetupMsg{} }
	}

	switch p.step {
	case 0: // api key entry
		switch k.String() {
		case "enter":
			if strings.TrimSpace(p.apiKey) == "" {
				return p, nil
			}
			p.step = 1
			return p, nil
		case "backspace":
			if len(p.apiKey) > 0 {
				p.apiKey = trimLastRune(p.apiKey)
			}
		default:
			if len(k.Runes) > 0 {
				p.apiKey += string(k.Runes)
			}
		}
	case 1: // endpoint selection
		switch k.String() {
		case "tab":
			p.useCustom = !p.useCustom
		case "enter":
			p.step = 2
			return p, p.emitConfigured()
		case "backspace":
			if p.useCustom && len(p.customURL) > 0 {
				p.customURL = trimLastRune(p.customURL)
			}
		default:
			if p.useCustom && len(k.Runes) > 0 {
				p.customURL += string(k.Runes)
			}
		}
	}
	return p, nil
}

func (p *ProviderSetupDialog) emitConfigured() tea.Cmd {
	url := p.DefaultBaseURL
	if p.useCustom && strings.TrimSpace(p.customURL) != "" {
		url = strings.TrimSpace(p.customURL)
	}
	provider := p.Provider
	model := p.Model
	key := p.apiKey
	p.Show = false
	return func() tea.Msg {
		return ProviderConfiguredMsg{
			Provider: provider,
			Model:    model,
			APIKey:   key,
			BaseURL:  url,
		}
	}
}

func (p ProviderSetupDialog) View(totalWidth, totalHeight int) string {
	if !p.Show {
		return ""
	}
	t := theme.CurrentTheme()
	dim := lipgloss.NewStyle().Foreground(t.TextMuted())
	accent := lipgloss.NewStyle().Foreground(t.DialogAccent()).Bold(true)
	muted := lipgloss.NewStyle().Foreground(t.TextMuted()).Faint(true)
	hint := func(s string) string { return muted.Render(s) }

	var b strings.Builder
	b.WriteString(dim.Render("provider: "))
	b.WriteString(accent.Render(p.Provider))
	b.WriteString("    ")
	b.WriteString(dim.Render("model: "))
	b.WriteString(accent.Render(p.Model))
	b.WriteString("\n\n")

	switch p.step {
	case 0:
		b.WriteString(dim.Render("step 1/2 — paste API key"))
		b.WriteString("\n\n")
		display := MaskKey(p.apiKey, 5)
		if display == "" {
			display = muted.Render("(start typing)")
		}
		b.WriteString("  ")
		b.WriteString(display)
		b.WriteString("\n\n")
		b.WriteString(hint("enter to continue · esc to cancel"))
	case 1:
		b.WriteString(dim.Render("step 2/2 — endpoint"))
		b.WriteString("\n\n")
		defLine := "  default: " + p.DefaultBaseURL
		if !p.useCustom {
			defLine = accent.Render("> ") + "default: " + p.DefaultBaseURL
		}
		b.WriteString(defLine)
		b.WriteString("\n")
		customLine := "  or paste custom: "
		if p.useCustom {
			customLine = accent.Render("> ") + "custom: " + p.customURL
		}
		b.WriteString(customLine)
		b.WriteString("\n\n")
		b.WriteString(hint("tab to toggle custom · enter to save · esc to cancel"))
	case 2:
		b.WriteString(dim.Render("saving..."))
	}

	return p.BaseView(b.String(), totalWidth, totalHeight)
}

// trimLastRune removes the trailing rune from s (UTF-8 safe).
func trimLastRune(s string) string {
	if s == "" {
		return s
	}
	for i := len(s) - 1; i >= 0; i-- {
		if s[i]&0xC0 != 0x80 {
			return s[:i]
		}
	}
	return ""
}
